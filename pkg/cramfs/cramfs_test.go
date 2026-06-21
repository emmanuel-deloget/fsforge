package cramfs

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
	img, err := New(testDeps()).Format(dev, image.Params{Label: "cramvol"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()

	etc, _ := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644))
	root.Create("data.bin", tree.Bytes(sampleBytes(10000)), meta(0o644)) // multi-block
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

func TestSuperblock(t *testing.T) {
	dev := device.NewMem(8 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	if le.Uint32(b[0:]) != magic {
		t.Fatalf("magic = %#x", le.Uint32(b[0:]))
	}
	if string(b[16:32]) != signature {
		t.Errorf("signature = %q", b[16:32])
	}
	if le.Uint32(b[8:])&flagShiftedRoot == 0 {
		t.Errorf("shifted-root flag not set")
	}
	size := le.Uint32(b[4:])
	if size == 0 || size > uint32(len(b)) {
		t.Errorf("implausible size %d", size)
	}
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(8 << 20)
	d2 := device.NewMem(8 << 20)
	buildSample(t, d1)
	buildSample(t, d2)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different cramfs images")
	}
}
