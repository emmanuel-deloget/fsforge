// Package profile measures what building an image costs over time: how much
// heap fsforge holds, and how many bytes it has written, sampled together on one
// clock so the two can be plotted against each other.
//
// It exists to keep a claim honest. fsforge streams file contents rather than
// buffering them, so memory should be flat while the bytes written climb to the
// size of the image. That is easy to assert in a test and hard to feel; a graph
// of the two curves makes the shape of it obvious, and makes a regression
// obvious too.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

func main() {
	var (
		fsType  = flag.String("type", "ext4", "filesystem type to build")
		size    = flag.String("size", "4G", "image size for fixed-size engines")
		corpus  = flag.String("corpus", "", "directory holding the source tree; generated if empty or absent")
		target  = flag.Int64("corpus-bytes", 0, "content to generate, in bytes; derived from -size when unset")
		out     = flag.String("out", "profile.json", "where to write the samples; empty to skip")
		svgOut  = flag.String("svg", "", "where to write the chart, e.g. doc/profile.svg")
		keep    = flag.Bool("keep-corpus", true, "keep the generated corpus for the next run")
		imgPath = flag.String("image", "", "where to write the image (a temporary file if empty)")
		every   = flag.Duration("interval", 10*time.Millisecond, "sampling interval")
		seed    = flag.Int64("seed", 20260814, "corpus seed, so a run can be repeated")
	)
	flag.Parse()

	if err := run(opts{
		fsType: *fsType, size: *size, corpusDir: *corpus, imgPath: *imgPath,
		outPath: *out, svgPath: *svgOut, targetBytes: *target,
		every: *every, seed: *seed, keepCorpus: *keep,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "profile:", err)
		os.Exit(1)
	}
}

type opts struct {
	fsType, size       string
	corpusDir, imgPath string
	outPath, svgPath   string
	targetBytes        int64
	every              time.Duration
	seed               int64
	keepCorpus         bool
}

// corpusHeadroom is the share of an image a corpus may fill. The rest goes to
// inodes, block bitmaps, the journal, and the rounding of every small file up
// to a whole block — with tens of thousands of them that last one alone is
// worth a couple of percent. Filling an image to the brim fails with "no
// contiguous space", which is a true statement and a poor explanation.
const corpusHeadroom = 0.80

func run(o opts) error {
	corpusDir := o.corpusDir
	if corpusDir == "" {
		corpusDir = filepath.Join(os.TempDir(), "fsforge-profile-corpus")
	}

	// Default the corpus from the image rather than fixing both, so the two
	// cannot be set to values that do not fit each other.
	if o.targetBytes == 0 {
		total, err := fsforge.ParseSize(o.size)
		if err != nil {
			return fmt.Errorf("-size %q: %w", o.size, err)
		}
		o.targetBytes = int64(float64(total) * corpusHeadroom)
	}
	// Say what this is about to cost before it costs it. Between the corpus and
	// the image this wants several gigabytes, and finding that out from a full
	// disk half an hour later is a poor way to learn it.
	fmt.Printf("fsforge profile\n"+
		"  corpus   %s (%.1f GiB, generated once and reused)\n"+
		"  image    %s %s\n"+
		"  needs    about %.1f GiB free\n\n",
		corpusDir, float64(o.targetBytes)/(1<<30), o.size, o.fsType,
		float64(o.targetBytes)*2/(1<<30))

	stats, err := ensureCorpus(corpusDir, o.targetBytes, o.seed)
	if err != nil {
		return err
	}
	if !o.keepCorpus {
		defer os.RemoveAll(corpusDir)
	}
	fmt.Printf("corpus: %d files, %.2f GiB in %d strata\n",
		stats.Files, float64(stats.Bytes)/(1<<30), len(stats.Strata))

	fsType, size, imgPath, outPath := o.fsType, o.size, o.imgPath, o.outPath
	every := o.every
	if imgPath == "" {
		f, err := os.CreateTemp("", "fsforge-profile-*.img")
		if err != nil {
			return err
		}
		imgPath = f.Name()
		f.Close()
		defer os.Remove(imgPath)
	}

	run, err := measure(fsType, size, corpusDir, imgPath, every)
	if err != nil {
		// The engines report a full image in their own terms; say what it means
		// here, where the two sizes that produced it are in hand.
		if strings.Contains(err.Error(), "contiguous space") || strings.Contains(err.Error(), "no space") {
			return fmt.Errorf("%w\n\n%.2f GiB of content does not fit in a %s image: "+
				"an image also holds inodes, bitmaps and a journal, and every small file "+
				"rounds up to a whole block.\nEither raise -size, or lower -corpus-bytes "+
				"(the default leaves %.0f%% headroom).",
				err, float64(stats.Bytes)/(1<<30), size, (1-corpusHeadroom)*100)
		}
		return err
	}
	run.Corpus = stats

	fmt.Printf("built %s in %.1fs: %.2f GiB written, held %.1f MiB (allocated peak %.1f MiB)\n",
		fsType, run.DurationSeconds, float64(run.BytesWritten)/(1<<30),
		float64(run.PeakLive)/(1<<20), float64(run.PeakHeap)/(1<<20))

	if outPath != "" {
		b, err := json.MarshalIndent(run, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(outPath, b, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s (%d samples)\n", outPath, len(run.Samples))
	}
	if o.svgPath == "" {
		fmt.Println("\nadd -svg doc/profile.svg to draw the chart")
	}
	if o.svgPath != "" {
		if err := writeSVG(run, o.svgPath); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", o.svgPath)
	}
	return nil
}

// Run is everything one measurement produced.
type Run struct {
	Filesystem      string      `json:"filesystem"`
	ImageSize       int64       `json:"imageSize"`
	BytesWritten    int64       `json:"bytesWritten"`
	PeakLive        uint64      `json:"peakLive"`
	PeakHeap        uint64      `json:"peakHeap"`
	DurationSeconds float64     `json:"durationSeconds"`
	IntervalMillis  int64       `json:"intervalMillis"`
	Corpus          CorpusStats `json:"corpus"`
	Samples         []Sample    `json:"samples"`
	Host            HostInfo    `json:"host"`
}

// Sample is one reading of both curves, on one clock.
type Sample struct {
	// Seconds since the build started.
	T float64 `json:"t"`
	// Live is heap memory occupied by objects that survived the last mark: what
	// fsforge is holding. This is the curve worth plotting.
	Live uint64 `json:"live"`
	// Heap is what has been allocated and not yet collected, garbage included.
	// Noisier, but its peak is what a machine needs to have free.
	Heap uint64 `json:"heap"`
	// Bytes written to the device so far, cumulative.
	Written int64 `json:"written"`
}

// measure builds the image, sampling both curves until it finishes.
func measure(fsType, size, corpusDir, imgPath string, every time.Duration) (Run, error) {
	run := Run{
		Filesystem:     fsType,
		IntervalMillis: every.Milliseconds(),
		Host:           hostInfo(),
	}

	// The image is written through a counting device so the second curve is
	// what the engine actually wrote, not how large the file looks: an image is
	// created sparse, and its apparent size says nothing about the work done.
	f, err := os.Create(imgPath)
	if err != nil {
		return run, err
	}
	defer f.Close()

	total, err := fsforge.ParseSize(size)
	if err != nil {
		return run, err
	}
	if err := f.Truncate(total); err != nil {
		return run, err
	}
	run.ImageSize = total

	// Whatever generating the corpus left on the heap is not fsforge's, and
	// starting the graph at a hundred megabytes of somebody else's garbage would
	// misread as a buffered build. Collect it before the first sample.
	runtime.GC()

	counter := &countingDevice{Device: device.NewFile(f, total)}
	eng, err := fsforge.EngineFor(fsType, fsforge.ReproducibleDeps(1600000000), 0)
	if err != nil {
		return run, err
	}

	sampler := startSampler(counter, every)
	start := time.Now()

	img, err := eng.Format(counter, image.Params{Label: "profile"})
	if err != nil {
		sampler.stop()
		return run, err
	}
	closer, err := fsforge.PopulateFromDir(img.Root(), corpusDir)
	if err != nil {
		closer.Close()
		sampler.stop()
		return run, err
	}
	if err := img.Finalize(); err != nil {
		closer.Close()
		sampler.stop()
		return run, err
	}
	run.DurationSeconds = time.Since(start).Seconds()
	closer.Close()

	run.Samples, run.PeakLive, run.PeakHeap = sampler.stop()
	run.BytesWritten = counter.total()
	return run, nil
}
