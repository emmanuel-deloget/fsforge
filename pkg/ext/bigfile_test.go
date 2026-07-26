package ext

import (
	"io"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// patternSource is a synthetic file of arbitrary size whose byte i is a pure
// function of i, so a test can build and verify multi-megabyte contents without
// holding them in memory.
type patternSource struct{ size int64 }

func patternByte(off int64) byte { return byte(off*7 + off>>11 + 0x5a) }

func (p patternSource) Size() int64 { return p.size }

func (p patternSource) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(b)), max(p.size-off, 0)))
	for i := 0; i < n; i++ {
		b[i] = patternByte(off + int64(i))
	}
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

// checkPattern verifies that src holds want bytes of the pattern.
func checkPattern(t *testing.T, src tree.Source, want int64) {
	t.Helper()
	if src == nil {
		t.Fatal("no content")
	}
	if got := src.Size(); got != want {
		t.Fatalf("size = %d, want %d", got, want)
	}
	buf := make([]byte, 64<<10)
	for off := int64(0); off < want; {
		n := int(min(int64(len(buf)), want-off))
		if _, err := src.ReadAt(buf[:n], off); err != nil && err != io.EOF {
			t.Fatalf("ReadAt(%d): %v", off, err)
		}
		for i := 0; i < n; i++ {
			if buf[i] != patternByte(off+int64(i)) {
				t.Fatalf("content mismatch at offset %d", off+int64(i))
			}
		}
		off += int64(n)
	}
}

// buildBig lays out an image holding a single large file and returns the device.
func buildBig(t *testing.T, e *Engine, devSize int64, bs uint32, fileSize int64) device.Device {
	t.Helper()
	dev := device.NewMem(devSize)
	img, err := e.Format(dev, image.Params{Label: "fsforge", BlockSize: bs})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if _, err := img.Root().Create("big", patternSource{size: fileSize}, meta(0o644)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return dev
}

// A file larger than one block group cannot fit in a single free run: per-group
// metadata caps every run at blocksPerGroup blocks, whatever the image size. It
// must therefore be chained out of several runs (regression for the "alloc: no
// contiguous space" failure on large files).
func TestFileLargerThanOneBlockGroup(t *testing.T) {
	const (
		bs       = 1024            // 8192 blocks (8 MiB) per group
		fileSize = 12 << 20        // spans two groups, so at least two runs
		devSize  = int64(96) << 20 //  plenty of free space beyond the file
	)
	for _, tc := range []struct {
		name string
		eng  *Engine
	}{
		{"ext2", NewExt2(testDeps())},
		{"ext4", NewExt4(testDeps())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dev := buildBig(t, tc.eng, devSize, bs, fileSize)

			opened, err := tc.eng.Open(dev)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			big := childByName(opened.(rootNoder).RootNode(), "big")
			if big == nil {
				t.Fatal("big missing")
			}
			checkPattern(t, big.Content, fileSize)
		})
	}
}

// One extent addresses at most extentMaxLen blocks (ee_len is 16-bit), so a
// longer physical run has to be cut into several leaves.
func TestContiguousRunsSplitAtExtentMax(t *testing.T) {
	const n = extentMaxLen + 5000
	data := make([]uint64, n)
	for i := range data {
		data[i] = uint64(i + 1000)
	}
	runs := contiguousRuns(data)
	if len(runs) != 2 {
		t.Fatalf("got %d leaves, want 2", len(runs))
	}
	want := []extentLeaf{
		{logical: 0, length: extentMaxLen, start: 1000},
		{logical: extentMaxLen, length: 5000, start: 1000 + extentMaxLen},
	}
	for i := range want {
		if runs[i] != want[i] {
			t.Errorf("leaf %d = %+v, want %+v", i, runs[i], want[i])
		}
	}
}

// i_size is a 32-bit field: rather than truncate silently, refuse the file.
func TestFileTooLarge(t *testing.T) {
	dev := device.NewMem(8 << 20)
	img, err := NewExt4(testDeps()).Format(dev, image.Params{BlockSize: 1024})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if _, err := img.Root().Create("huge", patternSource{size: 5 << 30}, meta(0o644)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := img.Finalize(); err != errFileTooLarge {
		t.Fatalf("Finalize: got %v, want errFileTooLarge", err)
	}
}
