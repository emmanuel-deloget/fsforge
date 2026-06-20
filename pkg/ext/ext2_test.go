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
	t.Helper()
	e := NewExt2(testDeps())
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
		g, err := computeGeometry(64<<20, bs)
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
