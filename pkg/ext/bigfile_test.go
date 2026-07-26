package ext

import (
	"crypto/sha256"
	"encoding/binary"
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

// inoByName resolves a root-level name to its inode number.
func inoByName(t *testing.T, dev device.Device, name string) uint32 {
	t.Helper()
	r, err := newReader(dev)
	if err != nil {
		t.Fatalf("newReader: %v", err)
	}
	root, err := r.readInode(rootIno)
	if err != nil {
		t.Fatalf("readInode(root): %v", err)
	}
	blocks, err := r.blockList(root, uint64(root.size))
	if err != nil {
		t.Fatalf("blockList(root): %v", err)
	}
	var found uint32
	for _, blk := range blocks {
		buf, err := r.readBlock(blk)
		if err != nil {
			t.Fatalf("readBlock: %v", err)
		}
		parseDirBlock(buf, func(ino uint32, n string, _ byte) {
			if n == name {
				found = ino
			}
		})
	}
	if found == 0 {
		t.Fatalf("%q not found in root", name)
	}
	return found
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

// The extent tree must grow past the four entries that fit in i_block: a file
// spread over more runs than that needs index nodes of its own.
func TestExtentTreeSpillsToNodes(t *testing.T) {
	const (
		bs       = 1024
		fileSize = 40 << 20 // ~5 groups, hence at least 5 extents
		devSize  = int64(96) << 20
	)
	dev := buildBig(t, NewExt4(testDeps()), devSize, bs, fileSize)

	r, err := newReader(dev)
	if err != nil {
		t.Fatalf("newReader: %v", err)
	}
	in, err := r.readInode(inoByName(t, dev, "big"))
	if err != nil {
		t.Fatalf("readInode: %v", err)
	}
	if in.flags&extentsFL == 0 {
		t.Fatal("inode does not use extents")
	}
	if depth := binary.LittleEndian.Uint16(in.blockRaw[6:]); depth == 0 {
		t.Fatalf("extent tree depth = 0, want an indexed tree for a %d-byte file", fileSize)
	}
	// i_blocks must account for the extent nodes on top of the data blocks.
	data := ceilDiv(uint64(fileSize), bs)
	if want := data * uint64(sectorsPerBlock(bs)); uint64(in.blocks) <= want {
		t.Errorf("i_blocks = %d, want more than the %d data sectors", in.blocks, want)
	}

	opened, err := NewExt4(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	checkPattern(t, childByName(opened.(rootNoder).RootNode(), "big").Content, fileSize)
}

// buildExtentTree must round-trip through parseExtents at every depth, including
// the deeply fragmented case where index nodes themselves need indexing.
func TestExtentTreeRoundTrip(t *testing.T) {
	const bs = 1024 // 84 entries per node, 4 inline: depth 2 past 336 extents
	for _, tc := range []struct {
		name      string
		runs      int
		wantDepth uint16
	}{
		{"inline", 3, 0},
		{"one level", 50, 1},
		{"two levels", 400, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dev := device.NewMem(8 << 20)
			img, err := NewExt4(testDeps()).Format(dev, image.Params{BlockSize: bs})
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			l := img.(*ext2Image).newLayouter(dev)
			// Every other block, so each data block is its own extent.
			data := make([]uint64, tc.runs)
			for i := range data {
				data[i] = uint64(2*i + 100)
			}
			root, err := l.buildExtentTree(data)
			if err != nil {
				t.Fatalf("buildExtentTree: %v", err)
			}
			if got := binary.LittleEndian.Uint16(root[6:]); got != tc.wantDepth {
				t.Errorf("depth = %d, want %d", got, tc.wantDepth)
			}
			if got := binary.LittleEndian.Uint16(root[4:]); got != inlineExtents {
				t.Errorf("root eh_max = %d, want %d", got, inlineExtents)
			}
			got, err := parseExtents(root, func(b uint64) ([]byte, error) {
				buf := make([]byte, bs)
				_, err := dev.ReadAt(buf, int64(b)*bs)
				return buf, err
			})
			if err != nil {
				t.Fatalf("parseExtents: %v", err)
			}
			if len(got) != len(data) {
				t.Fatalf("got %d blocks, want %d", len(got), len(data))
			}
			for i := range got {
				if got[i] != data[i] {
					t.Fatalf("block[%d] = %d, want %d", i, got[i], data[i])
				}
			}
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

// A large file must survive an offline mutation unchanged, which exercises the
// staged re-layout reading it back through its own extent tree.
func TestLargeFileSurvivesMutation(t *testing.T) {
	const (
		bs       = 1024
		fileSize = 12 << 20
	)
	dev := buildBig(t, NewExt4(testDeps()), 96<<20, bs, fileSize)

	opened, err := NewExt4(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := opened.Root().Create("added.txt", tree.Bytes("mutated\n"), meta(0o644)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := opened.Finalize(); err != nil {
		t.Fatalf("Finalize (mutate): %v", err)
	}

	again, err := NewExt4(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	root := again.(rootNoder).RootNode()
	checkPattern(t, childByName(root, "big").Content, fileSize)
	if got := string(readAll(t, childByName(root, "added.txt").Content)); got != "mutated\n" {
		t.Errorf("added.txt = %q", got)
	}
}

// Two builds of the same tree must produce byte-identical images, multi-run
// allocation and extent nodes included.
func TestLargeFileReproducible(t *testing.T) {
	const (
		bs       = 1024
		fileSize = 40 << 20
	)
	sum := func() [32]byte {
		dev := buildBig(t, NewExt4(testDeps()), 96<<20, bs, fileSize)
		buf := make([]byte, dev.Size())
		if _, err := dev.ReadAt(buf, 0); err != nil && err != io.EOF {
			t.Fatalf("ReadAt: %v", err)
		}
		return sha256.Sum256(buf)
	}
	if a, b := sum(), sum(); a != b {
		t.Fatalf("images differ: %x vs %x", a, b)
	}
}
