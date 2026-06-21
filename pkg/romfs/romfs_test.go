package romfs

import (
	"bytes"
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

func sampleBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

func buildSample(t *testing.T, dev device.Device) {
	t.Helper()
	img, err := New(testDeps()).Format(dev, image.Params{Label: "romvol"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()

	etc, _ := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644))
	root.Create("data.bin", tree.Bytes(sampleBytes(5000)), meta(0o644))
	root.Create("run.sh", tree.Bytes("echo hi\n"), meta(0o755)) // executable
	root.Create("empty", tree.Bytes(nil), meta(0o644))
	root.Symlink("link", "etc/hosts", meta(fs.ModeSymlink|0o777))
	root.Mknod("null", 1<<8|3, meta(fs.ModeCharDevice|0o666))
	a, _ := root.Mkdir("a", meta(fs.ModeDir|0o755))
	b, _ := a.(image.Dir).Mkdir("b", meta(fs.ModeDir|0o755))
	b.(image.Dir).Create("deep.txt", tree.Bytes("deep\n"), meta(0o644))

	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func TestSuperblock(t *testing.T) {
	dev := device.NewMem(8 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	if be.Uint32(b[0:]) != magicW0 || be.Uint32(b[4:]) != magicW1 {
		t.Fatalf("bad magic")
	}
	size := be.Uint32(b[8:])
	if size == 0 || size > uint32(len(b)) {
		t.Fatalf("implausible size %d", size)
	}
	if string(b[16:22]) != "romvol" {
		t.Errorf("volume name = %q", b[16:22])
	}
	// The kernel's mount check: the first 512 bytes (or whole image) sum to zero.
	n := 512
	if int(size) < n {
		n = int(size)
	}
	if checksum(b[:n]) != 0 {
		t.Errorf("superblock checksum does not validate (words do not sum to zero)")
	}
	// Root header at pos = superblock + volume name.
	pos := headerSize + paddedName("romvol")
	if ft := be.Uint32(b[pos:]) & typeMask; ft != typeDir {
		t.Errorf("root header type = %d, want dir", ft)
	}
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(8 << 20)
	d2 := device.NewMem(8 << 20)
	buildSample(t, d1)
	buildSample(t, d2)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different romfs images")
	}
}
