package cpio

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
	if _, err := root.Create("data.bin", tree.Bytes(sampleData(500)), meta(0o600)); err != nil {
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

	// A hard link: two names sharing one regular file.
	orig, err := root.Create("orig.txt", tree.Bytes("shared body\n"), meta(0o644))
	if err != nil {
		t.Fatalf("Create orig: %v", err)
	}
	if err := root.Link("hard.txt", orig); err != nil {
		t.Fatalf("Link: %v", err)
	}

	a, _ := root.Mkdir("a", meta(fs.ModeDir|0o755))
	b, _ := a.(image.Dir).Mkdir("b", meta(fs.ModeDir|0o755))
	if _, err := b.(image.Dir).Create("deep.txt", tree.Bytes("deep\n"), meta(0o644)); err != nil {
		t.Fatalf("Create deep: %v", err)
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
	s.ReadAt(b, 0)
	return b
}

func TestArchiveStructure(t *testing.T) {
	dev := device.NewMem(8 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	if string(b[:6]) != magicNewc {
		t.Fatalf("first magic = %q", b[:6])
	}
	if !bytes.Contains(b, []byte(trailerName)) {
		t.Errorf("no TRAILER!!! sentinel")
	}
	// Every newc header starts 4-byte aligned: the first body offset must be too.
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(8 << 20)
	d2 := device.NewMem(8 << 20)
	buildSample(t, d1)
	buildSample(t, d2)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different cpio archives")
	}
}

func TestOpenRoundTrip(t *testing.T) {
	dev := device.NewMem(8 << 20)
	buildSample(t, dev)

	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	root := opened.(interface{ RootNode() *image.Node }).RootNode()

	if etc := find(root, "etc"); etc == nil || string(readAll(t, find(etc, "hosts").Content)) != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts mismatch")
	}
	if got := readAll(t, find(root, "data.bin").Content); !bytes.Equal(got, sampleData(500)) {
		t.Errorf("data.bin mismatch: %d bytes", len(got))
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

	// The hard link must be recovered as one shared node.
	orig, hard := find(root, "orig.txt"), find(root, "hard.txt")
	if orig == nil || hard == nil {
		t.Fatal("hard-link names missing")
	}
	if orig != hard {
		t.Errorf("hard link not folded onto a shared node")
	}
	if string(readAll(t, orig.Content)) != "shared body\n" {
		t.Errorf("hard-link body = %q", readAll(t, orig.Content))
	}
}

func TestManyFiles(t *testing.T) {
	dev := device.NewMem(16 << 20)
	img, _ := New(testDeps()).Format(dev, image.Params{})
	root := img.Root()
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("file-%04d", i)
		root.Create(name, tree.Bytes([]byte(name)), meta(0o644))
	}
	if err := img.Finalize(); err != nil {
		t.Fatal(err)
	}

	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatal(err)
	}
	root2 := opened.(interface{ RootNode() *image.Node }).RootNode()
	if len(root2.Children) != 200 {
		t.Fatalf("got %d children", len(root2.Children))
	}
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("file-%04d", i)
		if c := find(root2, name); c == nil || string(readAll(t, c.Content)) != name {
			t.Fatalf("%s lost", name)
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
