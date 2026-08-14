package main

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runOf builds a small synthetic Run for the drawing code, so the chart can be
// tested without spending gigabytes on a corpus.
func runOf(n int) Run {
	r := Run{
		Filesystem: "ext4", ImageSize: 1 << 30, IntervalMillis: 10,
		DurationSeconds: 4,
		Corpus: CorpusStats{Files: 1000, Bytes: 900 << 20, Strata: []Stratum{
			{Name: "small", TargetBytes: 100 << 20},
			{Name: "large", TargetBytes: 800 << 20},
		}},
		Host: HostInfo{GOOS: "linux", GOARCH: "amd64", NumCPU: 8, Version: "go1.26.0"},
	}
	for i := 0; i < n; i++ {
		f := float64(i) / float64(n-1)
		var written int64
		if f > 0.25 { // nothing is written while the tree is built
			written = int64((f - 0.25) / 0.75 * float64(900<<20))
		}
		live := uint64(float64(50<<20) * min(1, f/0.3))
		r.Samples = append(r.Samples, Sample{T: f * 4, Live: live, Heap: live * 3 / 2, Written: written})
	}
	r.PeakLive, r.PeakHeap = 50<<20, 75<<20
	r.BytesWritten = r.Samples[len(r.Samples)-1].Written
	return r
}

func TestWriteSVG(t *testing.T) {
	out := filepath.Join(t.TempDir(), "p.svg")
	if err := writeSVG(runOf(200), out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	svg := string(b)

	// A chart in a README has to survive GitHub's sanitiser and be readable
	// without a runtime, so: no script, and a title for a screen reader.
	if strings.Contains(svg, "<script") {
		t.Error("the chart must not carry script")
	}
	for _, want := range []string{"<title", "<desc", "memory held", "bytes written", "tree built"} {
		if !strings.Contains(svg, want) {
			t.Errorf("chart is missing %q", want)
		}
	}
	// Both themes have to be defined, or half the readers get one theme's ink on
	// the other's ground.
	if !strings.Contains(svg, "prefers-color-scheme:dark") {
		t.Error("no dark-mode palette")
	}
	if strings.Contains(svg, "NaN") || strings.Contains(svg, "Inf") {
		t.Error("the geometry produced a non-finite number")
	}
}

func TestWriteSVGRefusesTooFewSamples(t *testing.T) {
	if err := writeSVG(Run{}, filepath.Join(t.TempDir(), "x.svg")); err == nil {
		t.Error("a run with no samples should not produce a chart")
	}
}

// TestTransitionsComeFromTheCorpus pins the choice to read the second mark off
// the corpus plan rather than infer it from the curve: inference moved between
// runs and put the label in the wrong place.
func TestTransitionsComeFromTheCorpus(t *testing.T) {
	r := runOf(200)
	got := transitions(r.Samples, r.Corpus)
	if len(got) != 2 {
		t.Fatalf("got %d transitions, want 2: %+v", len(got), got)
	}
	if got[0].label != "tree built" || got[0].t <= 0 {
		t.Errorf("first transition is %+v", got[0])
	}
	if got[1].label != "large files" {
		t.Errorf("second transition is %+v", got[1])
	}
	if got[1].t <= got[0].t {
		t.Error("the transitions are out of order")
	}
	// With one stratum there is no boundary to mark.
	r.Corpus.Strata = r.Corpus.Strata[:1]
	if n := len(transitions(r.Samples, r.Corpus)); n != 1 {
		t.Errorf("one stratum should give one transition, got %d", n)
	}
}

func TestPlateauLevel(t *testing.T) {
	r := runOf(200)
	got := plateauLevel(r.Samples)
	if got < 49<<20 || got > 51<<20 {
		t.Errorf("plateau = %d MiB, want about 50", got>>20)
	}
	if plateauLevel(nil) != 0 {
		t.Error("no samples should give no plateau")
	}
}

func TestNiceCeil(t *testing.T) {
	// 0.4 is its own ceiling: it divides into four steps of 0.1 with nothing
	// wasted, which is what the function is for. Expecting 0.5 here was the
	// test being wrong rather than the code.
	cases := map[float64]float64{0.4: 0.4, 0.42: 0.5, 3.2: 4, 54: 60, 91: 100, 3.69: 4, 0: 1}
	for in, want := range cases {
		if got := niceCeil(in, 4); got != want {
			t.Errorf("niceCeil(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestTimeTicks(t *testing.T) {
	ticks := timeTicks(7.8)
	if len(ticks) < 3 {
		t.Fatalf("too few ticks: %v", ticks)
	}
	if ticks[0] != 0 || ticks[len(ticks)-1] != 7.8 {
		t.Errorf("ticks should span the range: %v", ticks)
	}
	for i := 1; i < len(ticks); i++ {
		if ticks[i] <= ticks[i-1] {
			t.Errorf("ticks are not increasing: %v", ticks)
		}
	}
}

// TestStrataSumToTarget checks the corpus plan asks for what it was told to:
// deriving file counts from a mean size used to leave the real total wherever
// the random draws fell.
func TestStrataSumToTarget(t *testing.T) {
	const target = 4 << 30
	var sum int64
	for _, s := range strataFor(target) {
		sum += s.TargetBytes
	}
	if d := float64(sum-target) / target; d < -0.01 || d > 0.01 {
		t.Errorf("strata sum to %d, want %d (%.1f%% off)", sum, int64(target), d*100)
	}
}

func TestEnsureCorpusGeneratesAndReuses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "corpus")
	const target = 2 << 20

	stats, err := ensureCorpus(dir, target, 1)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files == 0 || stats.Bytes < target*9/10 {
		t.Fatalf("generated %d files, %d bytes for a %d target", stats.Files, stats.Bytes, int64(target))
	}

	// Second call must reuse rather than regenerate: on a real corpus that is
	// the difference between seconds and minutes.
	marker := filepath.Join(dir, "usr", "share", "marker")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureCorpus(dir, target, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the corpus was regenerated when it should have been reused")
	}
}

func TestCorpusIsDeterministic(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	s1, err := ensureCorpus(a, 1<<20, 7)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ensureCorpus(b, 1<<20, 7)
	if err != nil {
		t.Fatal(err)
	}
	if s1.Files != s2.Files || s1.Bytes != s2.Bytes {
		t.Errorf("same seed gave different corpora: %+v vs %+v", s1, s2)
	}
}

func TestFillLikeAFileIsCompressible(t *testing.T) {
	buf := make([]byte, 1<<16)
	fillLikeAFile(rand.New(rand.NewSource(1)), buf)

	// Compress it and look, rather than counting distinct byte values: the
	// sprinkle of noise touches all 256 of those while leaving the buffer highly
	// compressible, so the count says nothing. What matters is that a squashfs
	// benchmark built on this corpus is not measuring its worst case.
	var out bytes.Buffer
	zw, err := flate.NewWriter(&out, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(buf); err != nil {
		t.Fatal(err)
	}
	zw.Close()

	ratio := float64(out.Len()) / float64(len(buf))
	if ratio > 0.6 {
		t.Errorf("compresses to %.0f%% of its size; the corpus looks random, not file-like", ratio*100)
	}
	t.Logf("corpus bytes compress to %.0f%%", ratio*100)
}

func TestCountingDeviceCountsWrites(t *testing.T) {
	c := &countingDevice{Device: memDevice(make([]byte, 4096))}
	if _, err := c.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteAt([]byte("world"), 16); err != nil {
		t.Fatal(err)
	}
	if got := c.total(); got != 10 {
		t.Errorf("counted %d bytes, want 10", got)
	}
	// Discard releases space; it is the opposite of writing and must not count.
	if err := c.Discard(0, 512); err != nil {
		t.Fatal(err)
	}
	if got := c.total(); got != 10 {
		t.Errorf("Discard changed the count to %d", got)
	}
}

// memDevice is the smallest device that satisfies the interface.
type memDevice []byte

func (m memDevice) Size() int64 { return int64(len(m)) }
func (m memDevice) ReadAt(p []byte, off int64) (int, error) {
	return copy(p, m[off:]), nil
}
func (m memDevice) WriteAt(p []byte, off int64) (int, error) {
	return copy(m[off:], p), nil
}

func TestRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "p.json")
	svg := filepath.Join(dir, "p.svg")

	err := run(opts{
		fsType: "ext4", size: "32M",
		corpusDir: filepath.Join(dir, "corpus"), imgPath: filepath.Join(dir, "img"),
		outPath: out, svgPath: svg, targetBytes: 4 << 20,
		every: 1000000, seed: 3, keepCorpus: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var got Run
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.BytesWritten == 0 || len(got.Samples) < 2 || got.PeakLive == 0 {
		t.Errorf("measurement looks empty: %d bytes, %d samples, %d live",
			got.BytesWritten, len(got.Samples), got.PeakLive)
	}
	// What is held must be a fraction of what is written — that is the claim the
	// whole exercise exists to check.
	if got.PeakLive > uint64(got.BytesWritten) {
		t.Errorf("held %d bytes to write %d; nothing is being streamed",
			got.PeakLive, got.BytesWritten)
	}
	if _, err := os.Stat(svg); err != nil {
		t.Errorf("no chart was written: %v", err)
	}
}

func TestRunRejectsUnknownFilesystem(t *testing.T) {
	dir := t.TempDir()
	err := run(opts{
		fsType: "nosuchfs", size: "32M",
		corpusDir: filepath.Join(dir, "corpus"), imgPath: filepath.Join(dir, "img"),
		targetBytes: 1 << 20, every: 1000000, seed: 1, keepCorpus: true,
	})
	if err == nil {
		t.Error("an unknown filesystem should fail")
	}
}

// TestCorpusDefaultsFitTheImage is the bug a first user hit within a minute:
// `go run ./internal/profile` with no arguments generated a corpus exactly as
// large as the image and failed with "no contiguous space". Defaults that do
// not work are worse than no defaults.
func TestCorpusDefaultsFitTheImage(t *testing.T) {
	dir := t.TempDir()
	err := run(opts{
		fsType: "ext4", size: "64M", // targetBytes left at zero: derive it
		corpusDir: filepath.Join(dir, "corpus"), imgPath: filepath.Join(dir, "img"),
		every: 2000000, seed: 5, keepCorpus: true,
	})
	if err != nil {
		t.Fatalf("the default corpus should fit its image: %v", err)
	}
}

// TestOversizedCorpusExplainsItself checks the failure says what to do, since
// the engine's own wording ("no contiguous space") does not.
func TestOversizedCorpusExplainsItself(t *testing.T) {
	dir := t.TempDir()
	err := run(opts{
		fsType: "ext4", size: "16M", targetBytes: 20 << 20,
		corpusDir: filepath.Join(dir, "corpus"), imgPath: filepath.Join(dir, "img"),
		every: 2000000, seed: 5, keepCorpus: true,
	})
	if err == nil {
		t.Fatal("a corpus larger than its image should fail")
	}
	for _, want := range []string{"does not fit", "-size", "-corpus-bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q: %v", want, err)
		}
	}
}

// TestCorpusIsNotReusedWhenTooLarge covers the second half of the same bug: a
// corpus left behind by a bigger run was reused and then failed to fit.
func TestCorpusIsNotReusedWhenTooLarge(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "corpus")
	big, err := ensureCorpus(dir, 8<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	small, err := ensureCorpus(dir, 1<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if small.Bytes >= big.Bytes {
		t.Errorf("an oversized corpus was reused: %d bytes for a 1 MiB target", small.Bytes)
	}
}
