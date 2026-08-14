package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
)

// The corpus is laid out in three strata, in the order a build walks them, so
// the write curve changes slope where the workload changes character: many tiny
// files, where per-file metadata dominates; a middle band of ordinary binaries;
// and a tail of large files, where throughput is all that matters. A uniform
// corpus would prove the same thing about memory and show none of that.
//
// The shape is a rootfs's, roughly. A Debian install really does have tens of
// thousands of files under a few kilobytes and a handful of very large ones.

// Stratum describes one band of the corpus.
type Stratum struct {
	Name        string  `json:"name"`
	Dir         string  `json:"dir"`
	Files       int     `json:"files"`
	MinBytes    int     `json:"minBytes"`
	MaxBytes    int     `json:"maxBytes"`
	Share       float64 `json:"share"` // of the target total
	TargetBytes int64   `json:"targetBytes"`
}

// CorpusStats is what a generated corpus turned out to be.
type CorpusStats struct {
	Files  int       `json:"files"`
	Bytes  int64     `json:"bytes"`
	Strata []Stratum `json:"strata"`
}

// strataFor returns the plan for a target size. Each stratum is given a share
// of the total in bytes rather than a file count, so the shape holds at any
// size and the total lands where it was asked to: deriving counts from a mean
// file size leaves the real total wherever the random draws happen to fall.
func strataFor(target int64) []Stratum {
	plan := []Stratum{
		{Name: "small", Dir: "usr/share", MinBytes: 64, MaxBytes: 4 << 10, Share: 0.03},
		{Name: "medium", Dir: "usr/lib", MinBytes: 4 << 10, MaxBytes: 1 << 20, Share: 0.12},
		{Name: "large", Dir: "var/cache", MinBytes: 8 << 20, MaxBytes: 96 << 20, Share: 0.85},
	}
	for i := range plan {
		plan[i].TargetBytes = int64(float64(target) * plan[i].Share)
	}
	return plan
}

// ensureCorpus generates the tree if it is not already there, and reports what
// it holds either way. Regenerating four gigabytes on every run would dwarf the
// measurement it is meant to feed.
func ensureCorpus(dir string, target int64, seed int64) (CorpusStats, error) {
	plan := strataFor(target)
	stats := CorpusStats{Strata: plan}

	// Reuse only a corpus of about the right size. One left over from a larger
	// run would be reused happily and then fail to fit the image, which is a
	// confusing way to discover that a previous invocation asked for more.
	if have, err := corpusSize(dir); err == nil && have > target*9/10 && have < target*11/10 {
		stats.Bytes = have
		stats.Files = countFiles(dir)
		fmt.Printf("reusing the corpus already in %s\n", dir)
		return stats, nil
	}

	if err := os.RemoveAll(dir); err != nil {
		return stats, err
	}
	fmt.Printf("generating corpus (this is the slow part, and it only happens once)\n")
	rnd := rand.New(rand.NewSource(seed))
	for i := range plan {
		s := &plan[i]
		fmt.Printf("  %-7s %5.2f GiB in %s/ ... ", s.Name, float64(s.TargetBytes)/(1<<30), s.Dir)
		n, b, err := writeStratum(filepath.Join(dir, s.Dir), s, rnd)
		if err == nil {
			fmt.Printf("%d files\n", n)
		}
		if err != nil {
			return stats, fmt.Errorf("stratum %s: %w", s.Name, err)
		}
		stats.Files += n
		stats.Bytes += b
	}
	return stats, nil
}

func writeStratum(dir string, s *Stratum, rnd *rand.Rand) (int, int64, error) {
	var files int
	var bytes int64

	// One modest buffer, repeated, rather than one the size of the largest file:
	// allocating ninety-six megabytes here would still be on the heap when the
	// measurement starts and would be read as fsforge holding it.
	const chunk = 1 << 20
	buf := make([]byte, chunk)
	fillLikeAFile(rnd, buf)

	for i := 0; bytes < s.TargetBytes; i++ {
		// Fan out, so no single directory holds tens of thousands of entries —
		// that is a different test, and it would dominate this one.
		sub := filepath.Join(dir, fmt.Sprintf("%02x", i%256))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return files, bytes, err
		}
		size := s.MinBytes
		if s.MaxBytes > s.MinBytes {
			size += rnd.Intn(s.MaxBytes - s.MinBytes)
		}
		if rem := s.TargetBytes - bytes; int64(size) > rem {
			size = int(rem)
		}
		p := filepath.Join(sub, fmt.Sprintf("%s-%06d", s.Name, i))
		if err := writeSized(p, buf, size, rnd); err != nil {
			return files, bytes, err
		}
		files++
		bytes += int64(size)
	}
	s.Files = files
	return files, bytes, nil
}

// writeSized writes a file of the given size by repeating the buffer from a
// rotating offset, so no two files are byte-identical — otherwise a
// deduplicating engine would be measured flattering itself.
func writeSized(path string, buf []byte, size int, rnd *rand.Rand) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	off := rnd.Intn(len(buf))
	for written := 0; written < size; {
		if off >= len(buf) {
			off = 0
		}
		n := min(len(buf)-off, size-written)
		if _, err := f.Write(buf[off : off+n]); err != nil {
			return err
		}
		written += n
		off += n
	}
	return nil
}

// fillLikeAFile gives the buffer the redundancy real files have. Random bytes
// would measure a compressing engine at its worst case and say nothing about a
// rootfs.
func fillLikeAFile(rnd *rand.Rand, buf []byte) {
	words := [][]byte{
		[]byte("/usr/lib/x86_64-linux-gnu/libsystemd.so.0.38.0\n"),
		[]byte("\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
		[]byte("the quick brown fox jumps over the lazy dog "),
		[]byte("0123456789abcdef"),
		make([]byte, 128), // padding, as a linker leaves
	}
	for i := 0; i < len(buf); {
		if rnd.Intn(24) == 0 {
			n := min(256, len(buf)-i)
			rnd.Read(buf[i : i+n])
			i += n
			continue
		}
		i += copy(buf[i:], words[rnd.Intn(len(words))])
	}
}

func corpusSize(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return total, err
}

func countFiles(dir string) int {
	var n int
	filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n++
		}
		return nil
	})
	return n
}
