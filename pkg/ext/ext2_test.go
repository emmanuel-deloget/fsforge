package ext

import (
	"bytes"
	"io"
	"io/fs"
	"testing"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func testDeps() image.Deps {
	return image.Deps{
		Clock: image.FixedClock{T: time.Unix(1_700_000_000, 0).UTC()},
		UUID:  image.FixedUUID{V: [16]byte{0xfe, 0xed, 0xfa, 0xce, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}},
	}
}

func meta(mode fs.FileMode) tree.Meta {
	return tree.Meta{Mode: mode, UID: 1000, GID: 1000, ModTime: time.Unix(1_700_000_000, 0).UTC()}
}

// buildSample populates a representative tree and returns the closed image.
func buildSample(t *testing.T, dev device.Device, bigSize int) image.Image {
	return buildSampleWith(t, NewExt2(testDeps()), dev, bigSize)
}

func buildSampleWith(t *testing.T, e *Engine, dev device.Device, bigSize int) image.Image {
	t.Helper()
	img, err := e.Format(dev, image.Params{Label: "fsforge"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()

	etc, err := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	if err != nil {
		t.Fatalf("Mkdir etc: %v", err)
	}
	if _, err := etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644)); err != nil {
		t.Fatalf("Create hosts: %v", err)
	}

	big := bytes.Repeat([]byte("0123456789abcdef"), bigSize/16)
	f1, err := root.Create("bigfile", tree.Bytes(big), meta(0o644))
	if err != nil {
		t.Fatalf("Create bigfile: %v", err)
	}
	if err := root.Link("bigfile.hardlink", f1); err != nil {
		t.Fatalf("Link: %v", err)
	}

	if err := root.Symlink("shortlink", "etc/hosts", meta(fs.ModeSymlink|0o777)); err != nil {
		t.Fatalf("Symlink short: %v", err)
	}
	longTarget := string(bytes.Repeat([]byte("x"), 200))
	if err := root.Symlink("longlink", longTarget, meta(fs.ModeSymlink|0o777)); err != nil {
		t.Fatalf("Symlink long: %v", err)
	}

	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return img
}

type rootNoder interface{ RootNode() *image.Node }

func childByName(n *image.Node, name string) *image.Node {
	for _, e := range n.Children {
		if e.Name == name {
			return e.Node
		}
	}
	return nil
}

func readAll(t *testing.T, s tree.Source) []byte {
	t.Helper()
	if s == nil {
		return nil
	}
	buf := make([]byte, s.Size())
	if _, err := s.ReadAt(buf, 0); err != nil && err != io.EOF {
		t.Fatalf("ReadAt: %v", err)
	}
	return buf
}

func TestRoundTrip(t *testing.T) {
	const bigSize = 400 * 1024 // exceeds single-indirect with 1KiB blocks
	dev := device.NewMem(16 << 20)
	buildSample(t, dev, bigSize)

	opened, err := NewExt2(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	root := opened.(rootNoder).RootNode()

	if lf := childByName(root, "lost+found"); lf == nil || !lf.IsDir() {
		t.Errorf("lost+found missing or not a directory")
	}

	etc := childByName(root, "etc")
	if etc == nil || !etc.IsDir() {
		t.Fatalf("etc missing")
	}
	hosts := childByName(etc, "hosts")
	if hosts == nil {
		t.Fatalf("etc/hosts missing")
	}
	if got := string(readAll(t, hosts.Content)); got != "127.0.0.1 localhost\n" {
		t.Errorf("hosts content = %q", got)
	}

	big := childByName(root, "bigfile")
	if big == nil {
		t.Fatalf("bigfile missing")
	}
	want := bytes.Repeat([]byte("0123456789abcdef"), bigSize/16)
	if got := readAll(t, big.Content); !bytes.Equal(got, want) {
		t.Errorf("bigfile content mismatch: got %d bytes, want %d", len(got), len(want))
	}
	if big.Nlink != 2 {
		t.Errorf("bigfile nlink = %d, want 2", big.Nlink)
	}
	hl := childByName(root, "bigfile.hardlink")
	if hl == nil || !bytes.Equal(readAll(t, hl.Content), want) {
		t.Errorf("hardlink content mismatch")
	}

	short := childByName(root, "shortlink")
	if short == nil || short.Link != "etc/hosts" {
		t.Errorf("shortlink = %q", linkOf(short))
	}
	long := childByName(root, "longlink")
	if long == nil || long.Link != string(bytes.Repeat([]byte("x"), 200)) {
		t.Errorf("longlink mismatch (len %d)", len(linkOf(long)))
	}
}

func linkOf(n *image.Node) string {
	if n == nil {
		return ""
	}
	return n.Link
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(16 << 20)
	d2 := device.NewMem(16 << 20)
	buildSample(t, d1, 400*1024)
	buildSample(t, d2, 400*1024)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatalf("identical inputs produced different images")
	}
}

func TestExt4RoundTrip(t *testing.T) {
	const bigSize = 400 * 1024
	dev := device.NewMem(32 << 20)
	buildSampleWith(t, NewExt4(testDeps()), dev, bigSize)

	// Superblock must advertise extents + filetype and 256-byte inodes.
	raw := make([]byte, superblockSize)
	if _, err := dev.ReadAt(raw, superblockOffset); err != nil {
		t.Fatal(err)
	}
	sb := parseSuperblock(raw)
	if sb.featureIncompat&featIncompatExtents == 0 {
		t.Errorf("extents feature not set")
	}
	if sb.inodeSize != ext4InodeSize {
		t.Errorf("inode size = %d, want %d", sb.inodeSize, ext4InodeSize)
	}

	opened, err := NewExt4(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	root := opened.(rootNoder).RootNode()

	etc := childByName(root, "etc")
	if etc == nil {
		t.Fatal("etc missing")
	}
	if got := string(readAll(t, childByName(etc, "hosts").Content)); got != "127.0.0.1 localhost\n" {
		t.Errorf("hosts = %q", got)
	}
	want := bytes.Repeat([]byte("0123456789abcdef"), bigSize/16)
	if got := readAll(t, childByName(root, "bigfile").Content); !bytes.Equal(got, want) {
		t.Errorf("bigfile mismatch: %d vs %d bytes", len(got), len(want))
	}
	if l := childByName(root, "longlink"); l == nil || l.Link != string(bytes.Repeat([]byte("x"), 200)) {
		t.Errorf("longlink mismatch")
	}
}

// mutateAndCheck opens an image, adds and removes files, re-Finalizes, then
// reopens and verifies. The key assertion is that an unchanged large file keeps
// its exact contents despite block relocation — i.e. the staged re-layout did
// not overwrite blocks the lazy sources still needed.
func mutateAndCheck(t *testing.T, e *Engine, dev device.Device, bigSize int) {
	t.Helper()
	opened, err := e.Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	root := opened.Root()
	if _, err := root.Create("added.txt", tree.Bytes("hello from mutation\n"), meta(0o644)); err != nil {
		t.Fatalf("Create added: %v", err)
	}
	if err := root.Remove("shortlink"); err != nil {
		t.Fatalf("Remove shortlink: %v", err)
	}
	if err := opened.Finalize(); err != nil {
		t.Fatalf("Finalize (mutate): %v", err)
	}

	reopened, err := e.Open(dev)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	root2 := reopened.(rootNoder).RootNode()

	if n := childByName(root2, "added.txt"); n == nil || string(readAll(t, n.Content)) != "hello from mutation\n" {
		t.Errorf("added.txt missing or wrong")
	}
	if childByName(root2, "shortlink") != nil {
		t.Errorf("shortlink should be removed")
	}
	// The hazard check: bigfile content must be byte-for-byte intact.
	want := bytes.Repeat([]byte("0123456789abcdef"), bigSize/16)
	if got := readAll(t, childByName(root2, "bigfile").Content); !bytes.Equal(got, want) {
		t.Errorf("bigfile corrupted by mutation: %d vs %d bytes", len(got), len(want))
	}
	etc := childByName(root2, "etc")
	if etc == nil || string(readAll(t, childByName(etc, "hosts").Content)) != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts lost in mutation")
	}
}

func TestMutationExt2(t *testing.T) {
	dev := device.NewMem(16 << 20)
	buildSample(t, dev, 400*1024)
	mutateAndCheck(t, NewExt2(testDeps()), dev, 400*1024)
}

func TestMutationExt4(t *testing.T) {
	dev := device.NewMem(32 << 20)
	buildSampleWith(t, NewExt4(testDeps()), dev, 400*1024)
	mutateAndCheck(t, NewExt4(testDeps()), dev, 400*1024)
}

func TestExt4Reproducible(t *testing.T) {
	d1 := device.NewMem(32 << 20)
	d2 := device.NewMem(32 << 20)
	buildSampleWith(t, NewExt4(testDeps()), d1, 400*1024)
	buildSampleWith(t, NewExt4(testDeps()), d2, 400*1024)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different ext4 images")
	}
}

func TestSuperblockSane(t *testing.T) {
	dev := device.NewMem(16 << 20)
	buildSample(t, dev, 64*1024)

	raw := make([]byte, superblockSize)
	if _, err := dev.ReadAt(raw, superblockOffset); err != nil {
		t.Fatal(err)
	}
	sb := parseSuperblock(raw)
	if sb.magic != magic {
		t.Fatalf("magic = %#x", sb.magic)
	}
	if sb.firstIno != firstIno || sb.inodeSize != goodOldInodeSize {
		t.Errorf("firstIno=%d inodeSize=%d", sb.firstIno, sb.inodeSize)
	}
	if sb.featureIncompat&featIncompatFiletype == 0 {
		t.Errorf("filetype feature not set")
	}
	if sb.freeBlocksCount == 0 || sb.freeBlocksCount >= sb.blocksCount {
		t.Errorf("implausible free blocks: %d/%d", sb.freeBlocksCount, sb.blocksCount)
	}
	if sb.freeInodesCount == 0 || sb.freeInodesCount >= sb.inodesCount {
		t.Errorf("implausible free inodes: %d/%d", sb.freeInodesCount, sb.inodesCount)
	}
}

func TestGeometry(t *testing.T) {
	for _, bs := range []uint32{1024, 4096} {
		g, err := computeGeometry(64<<20, bs, 128)
		if err != nil {
			t.Fatalf("bs=%d: %v", bs, err)
		}
		if g.blockSize != bs {
			t.Errorf("blockSize=%d want %d", g.blockSize, bs)
		}
		wantFirst := uint64(0)
		if bs == 1024 {
			wantFirst = 1
		}
		if g.firstDataBlock != wantFirst {
			t.Errorf("bs=%d firstDataBlock=%d want %d", bs, g.firstDataBlock, wantFirst)
		}
		if g.inodesCount == 0 || g.numGroups == 0 {
			t.Errorf("bs=%d degenerate geometry %+v", bs, g)
		}
	}
}

// TestRuntFinalGroup guards issue #12: a device whose size leaves a small
// remainder past the last full block group must not produce a final group whose
// inode table overruns the group (the kernel's ext4_check_descriptors rejects
// it). computeGeometry drops such a runt group, mke2fs-style.
func TestRuntFinalGroup(t *testing.T) {
	const bs = 4096
	// The exact pathological size from the issue: 65792 blocks, last group only
	// 256 blocks while its inode table alone needs 343.
	sizes := []int64{65792 * bs}
	// Sweep sizes around a group boundary so a short trailing group is exercised
	// across the whole runt range, not just the one reported number.
	for blocks := int64(32769); blocks <= 32769+1024; blocks += 7 {
		sizes = append(sizes, blocks*bs)
	}
	for _, devSize := range sizes {
		g, err := computeGeometry(devSize, bs, 256)
		if err != nil {
			t.Fatalf("devSize=%d: %v", devSize, err)
		}
		for gr := uint64(0); gr < g.numGroups; gr++ {
			_, _, inodeTable, _ := g.groupLayout(gr)
			start := g.groupStart(gr)
			end := start + g.blocksInGroup(gr) - 1 // last block of the group
			if itEnd := inodeTable + g.inodeTableBlocks - 1; itEnd > end {
				t.Errorf("devSize=%d group %d: inode table ends at %d, past group end %d",
					devSize, gr, itEnd, end)
			}
			if end >= g.totalBlocks {
				t.Errorf("devSize=%d group %d: end %d past device (%d blocks)",
					devSize, gr, end, g.totalBlocks)
			}
		}
	}
}

func TestDirBlocksRoundTrip(t *testing.T) {
	entries := []dentry{
		{ino: 2, name: ".", ftype: ftDir},
		{ino: 2, name: "..", ftype: ftDir},
		{ino: 11, name: "lost+found", ftype: ftDir},
		{ino: 12, name: "averylongfilename-that-uses-space.txt", ftype: ftRegFile},
	}
	blocks := buildDirBlocks(entries, 1024)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	var got []dentry
	parseDirBlock(blocks[0], func(ino uint32, name string, ft byte) {
		got = append(got, dentry{ino: ino, name: name, ftype: ft})
	})
	if len(got) != len(entries) {
		t.Fatalf("parsed %d entries, want %d", len(got), len(entries))
	}
	for i := range entries {
		if got[i] != entries[i] {
			t.Errorf("entry %d: got %+v want %+v", i, got[i], entries[i])
		}
	}
}
