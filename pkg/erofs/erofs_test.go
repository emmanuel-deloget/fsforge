package erofs

import (
	"bytes"
	"fmt"
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
		UUID:  image.FixedUUID{},
	}
}

func meta(mode fs.FileMode) tree.Meta {
	return tree.Meta{Mode: mode, ModTime: time.Unix(1_700_000_000, 0).UTC()}
}

func sampleData(n int) []byte {
	return bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog.\n"), n)
}

func buildSample(t *testing.T, dev device.Device) {
	t.Helper()
	img, err := New(testDeps()).Format(dev, image.Params{Label: "fsforge"})
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

	// A file spanning several 4 KiB blocks.
	if _, err := root.Create("data.bin", tree.Bytes(sampleData(2000)), meta(0o600)); err != nil {
		t.Fatalf("Create data.bin: %v", err)
	}
	if _, err := root.Create("empty", tree.Bytes(nil), meta(0o644)); err != nil {
		t.Fatalf("Create empty: %v", err)
	}
	if err := root.Symlink("link", "etc/hosts", meta(fs.ModeSymlink|0o777)); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := root.Mknod("null", 1<<8|3, meta(fs.ModeCharDevice|0o666)); err != nil {
		t.Fatalf("Mknod: %v", err)
	}

	a, _ := root.Mkdir("a", meta(fs.ModeDir|0o755))
	b, _ := a.(image.Dir).Mkdir("b", meta(fs.ModeDir|0o755))
	if _, err := b.(image.Dir).Create("deep.txt", tree.Bytes("deep\n"), meta(0o644)); err != nil {
		t.Fatalf("Create deep: %v", err)
	}

	// A directory wide enough to span several directory blocks.
	wide, _ := root.Mkdir("wide", meta(fs.ModeDir|0o755))
	for i := 0; i < 300; i++ {
		name := fmt.Sprintf("file-%04d", i)
		if _, err := wide.(image.Dir).Create(name, tree.Bytes([]byte(name)), meta(0o644)); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func find(n *image.Node, name string) *image.Node {
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
		t.Fatal("nil source")
	}
	b := make([]byte, s.Size())
	if _, err := s.ReadAt(b, 0); err != nil && err.Error() != "EOF" {
		// ReadAt may legitimately return io.EOF on a full read.
	}
	return b
}

func TestSuperblockFields(t *testing.T) {
	dev := device.NewMem(32 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	sb, err := parseSuperblock(b[superOffset : superOffset+superSize])
	if err != nil {
		t.Fatalf("parseSuperblock: %v", err)
	}
	if b[superOffset+12] != blkSizeBits {
		t.Errorf("blkszbits = %d", b[superOffset+12])
	}
	if b[superOffset+13] != 0 {
		t.Errorf("sb_extslots must be 0, got %d", b[superOffset+13])
	}
	if got := le.Uint32(b[superOffset+80:]); got != 0 {
		t.Errorf("feature_incompat must be 0, got %#x", got)
	}
	if sb.rootNid == 0 {
		t.Errorf("root nid unset")
	}
	if sb.blocks == 0 || int(sb.blocks)*blockSize > len(b) {
		t.Errorf("implausible blocks %d", sb.blocks)
	}
	if string(bytes.TrimRight(sb.volumeName[:], "\x00")) != "fsforge" {
		t.Errorf("volume name = %q", sb.volumeName)
	}
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(32 << 20)
	d2 := device.NewMem(32 << 20)
	buildSample(t, d1)
	buildSample(t, d2)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different EROFS images")
	}
}

func TestOpenRoundTrip(t *testing.T) {
	dev := device.NewMem(32 << 20)
	buildSample(t, dev)

	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	root := opened.(interface{ RootNode() *image.Node }).RootNode()

	etc := find(root, "etc")
	if etc == nil || string(readAll(t, find(etc, "hosts").Content)) != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts mismatch")
	}
	if got := readAll(t, find(root, "data.bin").Content); !bytes.Equal(got, sampleData(2000)) {
		t.Errorf("data.bin mismatch: %d vs %d bytes", len(got), len(sampleData(2000)))
	}
	if e := find(root, "empty"); e == nil || e.Content == nil || e.Content.Size() != 0 {
		t.Errorf("empty file lost")
	}
	if ln := find(root, "link"); ln == nil || ln.Mode&fs.ModeSymlink == 0 || ln.Link != "etc/hosts" {
		t.Errorf("symlink lost: %+v", ln)
	}
	if dn := find(root, "null"); dn == nil || dn.Mode&fs.ModeCharDevice == 0 || dn.Rdev != 0x0103 {
		t.Errorf("device node lost: %+v", dn)
	}
	if d := find(find(find(root, "a"), "b"), "deep.txt"); d == nil || string(readAll(t, d.Content)) != "deep\n" {
		t.Errorf("nested file lost")
	}

	wide := find(root, "wide")
	if wide == nil || len(wide.Children) != 300 {
		t.Fatalf("wide directory: got %d children", len(wide.Children))
	}
	for i := 0; i < 300; i++ {
		name := fmt.Sprintf("file-%04d", i)
		c := find(wide, name)
		if c == nil || string(readAll(t, c.Content)) != name {
			t.Fatalf("wide/%s lost", name)
		}
	}
}

func TestModeRoundTrip(t *testing.T) {
	cases := []fs.FileMode{
		0o644, fs.ModeDir | 0o755, fs.ModeSymlink | 0o777,
		fs.ModeCharDevice | fs.ModeDevice | 0o666, fs.ModeDevice | 0o660,
		fs.ModeNamedPipe | 0o644, fs.ModeSocket | 0o755,
		fs.ModeSetuid | 0o755, fs.ModeSetgid | 0o755, fs.ModeSticky | 0o777,
	}
	for _, m := range cases {
		if got := modeFromUnix(modeToUnix(m)); got != m {
			t.Errorf("mode %v round-tripped to %v", m, got)
		}
	}
}
