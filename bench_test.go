package fsforge_test

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// The benchmarks answer the question somebody evaluating fsforge asks first:
// how long does a real rootfs take? A synthetic tree of a few thousand files
// with a size distribution like a distribution's own — mostly small, a long
// tail of large — is closer to that than a handful of tiny files would be.
//
// Run:
//
//	go test -run '^$' -bench . -benchmem .
//	go test -run '^$' -bench BenchmarkBuild/ext4 -benchtime 5x .
//
// The tree is built once per size and reused, so what is measured is the image
// writer rather than the test's own file creation.

// corpus sizes, in files.
var corpusSizes = []int{200, 2000}

// benchTree lays out a directory of n files whose sizes follow a rough
// distribution: many small ones, a few large. It returns the directory and the
// total bytes, and is cached across benchmarks that ask for the same n.
var treeCache = map[int]benchCorpus{}

type benchCorpus struct {
	dir   string
	bytes int64
}

func benchTree(b *testing.B, n int) benchCorpus {
	b.Helper()
	if c, ok := treeCache[n]; ok {
		return c
	}
	dir, err := os.MkdirTemp("", fmt.Sprintf("fsforge-bench-%d-*", n))
	if err != nil {
		b.Fatal(err)
	}
	rnd := rand.New(rand.NewSource(int64(n)))
	var total int64
	for i := 0; i < n; i++ {
		// A shallow-but-not-flat layout, as a rootfs has.
		sub := filepath.Join(dir, fmt.Sprintf("d%02d", i%16), fmt.Sprintf("s%02d", (i/16)%8))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			b.Fatal(err)
		}
		var size int
		switch r := rnd.Intn(100); {
		case r < 70:
			size = 1 + rnd.Intn(4096) // most files are small
		case r < 95:
			size = 4096 + rnd.Intn(64<<10)
		default:
			size = 256<<10 + rnd.Intn(1<<20) // a long tail of large ones
		}
		buf := fillLikeAFile(rnd, size)
		p := filepath.Join(sub, fmt.Sprintf("f%05d", i))
		if err := os.WriteFile(p, buf, 0o644); err != nil {
			b.Fatal(err)
		}
		total += int64(size)
	}
	c := benchCorpus{dir: dir, bytes: total}
	treeCache[n] = c
	return c
}

// fillLikeAFile produces bytes with the redundancy real files have. Filling
// with random bytes would measure every compressing engine at its worst case
// and report a size ratio above one, which says nothing about a rootfs: text,
// ELF and page-aligned padding all compress.
func fillLikeAFile(rnd *rand.Rand, size int) []byte {
	buf := make([]byte, size)
	words := [][]byte{
		[]byte("the quick brown fox "), []byte("/usr/lib/x86_64-linux-gnu/"),
		[]byte("\x7fELF\x02\x01\x01\x00"), []byte("0123456789abcdef"),
		make([]byte, 64), // a run of zeroes, as padding
	}
	for i := 0; i < len(buf); {
		if rnd.Intn(16) == 0 { // a little incompressible noise
			n := min(64, len(buf)-i)
			rnd.Read(buf[i : i+n])
			i += n
			continue
		}
		w := words[rnd.Intn(len(words))]
		i += copy(buf[i:], w)
	}
	return buf
}

// BenchmarkBuild measures a whole image build, per engine and corpus size.
// b.SetBytes makes the report read in MB/s of input, which is the number that
// compares across engines.
func BenchmarkBuild(b *testing.B) {
	// Fixed-size engines are given room proportional to the corpus, not a flat
	// gigabyte: the size ratio is only worth reporting when it measures the
	// format's overhead rather than the number the caller happened to pass.
	engines := []string{"ext4", "squashfs", "erofs", "cpio", "iso"}
	fixed := map[string]bool{"ext4": true}

	for _, n := range corpusSizes {
		corpus := benchTree(b, n)
		for _, fsType := range engines {
			b.Run(fmt.Sprintf("%s/%dfiles", fsType, n), func(b *testing.B) {
				size := ""
				if fixed[fsType] {
					size = fmt.Sprintf("%dM", max(16, corpus.bytes*2>>20))
				}
				out := filepath.Join(b.TempDir(), "image")
				b.SetBytes(corpus.bytes)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := fsforge.New(fsType).
						Reproducible(1600000000).
						Size(size).
						BuildFromDir(corpus.dir, out); err != nil {
						b.Fatalf("build: %v", err)
					}
				}
				b.StopTimer()
				if st, err := os.Stat(out); err == nil {
					b.ReportMetric(float64(st.Size())/float64(corpus.bytes), "ratio")
				}
			})
		}
	}
}

// BenchmarkReadBack measures the other direction, which matters for convert:
// opening an image and walking its tree.
func BenchmarkReadBack(b *testing.B) {
	for _, fsType := range []string{"ext4", "squashfs", "erofs"} {
		corpus := benchTree(b, 2000)
		size := ""
		if fsType == "ext4" {
			size = "1G"
		}
		img := filepath.Join(b.TempDir(), "image")
		if err := fsforge.New(fsType).Reproducible(1600000000).Size(size).
			BuildFromDir(corpus.dir, img); err != nil {
			b.Fatalf("build: %v", err)
		}
		b.Run(fsType, func(b *testing.B) {
			b.SetBytes(corpus.bytes)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				root := readBackB(b, fsType, img)
				if len(root.Children) == 0 {
					b.Fatal("read back an empty tree")
				}
			}
		})
	}
}

// readBackB opens an image for the benchmarks; readBack in spec_test.go takes a
// *testing.T, and the two cannot share a signature without a generic wrapper
// that would be more machinery than it saves.
func readBackB(b *testing.B, fstype, path string) *image.Node {
	b.Helper()
	f, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { f.Close() })
	fi, err := f.Stat()
	if err != nil {
		b.Fatal(err)
	}
	eng, err := fsforge.EngineFor(fstype, fsforge.HostDeps(), 0)
	if err != nil {
		b.Fatal(err)
	}
	img, err := eng.Open(device.NewFile(f, fi.Size()))
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	return img.(interface{ RootNode() *image.Node }).RootNode()
}
