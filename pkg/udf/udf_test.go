package udf

import (
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
	img, err := New(testDeps()).Format(dev, image.Params{Label: "FSFORGE"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()

	etc, err := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := root.Create("data.bin", tree.Bytes(sampleBytes(5000)), meta(0o644)); err != nil {
		t.Fatalf("Create data: %v", err)
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
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func TestStructure(t *testing.T) {
	dev := device.NewMem(8 << 20)
	buildSample(t, dev)
	b := dev.Bytes()

	// Volume Recognition Sequence at sector 16.
	if string(b[16*blockSize+1:16*blockSize+6]) != "BEA01" {
		t.Errorf("no BEA01 at sector 16")
	}
	if string(b[17*blockSize+1:17*blockSize+6]) != "NSR03" {
		t.Errorf("no NSR03 at sector 17")
	}
	if string(b[18*blockSize+1:18*blockSize+6]) != "TEA01" {
		t.Errorf("no TEA01 at sector 18")
	}
	// Anchor at 256 with the right tag.
	if le.Uint16(b[256*blockSize:]) != tagAVDP {
		t.Errorf("no anchor at 256")
	}
	// PVD at 20, FSD at the partition start.
	if le.Uint16(b[20*blockSize:]) != tagPVD {
		t.Errorf("no PVD at 20")
	}
	if le.Uint16(b[partBlock*blockSize:]) != tagFSD {
		t.Errorf("no FSD at partition start")
	}
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(8 << 20)
	d2 := device.NewMem(8 << 20)
	buildSample(t, d1)
	buildSample(t, d2)
	if string(d1.Bytes()) != string(d2.Bytes()) {
		t.Fatal("identical inputs produced different UDF images")
	}
}
