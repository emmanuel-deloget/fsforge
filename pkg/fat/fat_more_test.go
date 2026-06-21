package fat

import (
	"bytes"
	"io/fs"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// TestFAT12Build exercises the FAT12 path (packFAT12 and the 12-bit FAT write),
// which the FAT16/FAT32 samples do not reach.
func TestFAT12Build(t *testing.T) {
	dev := device.NewMem(2 << 20) // ~2 MiB: within FAT12's 4084-cluster ceiling
	img, err := New(testDeps(), WithFATBits(12)).Format(dev, image.Params{Label: "TINY"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()
	if _, err := root.Create("readme.txt", tree.Bytes("fat12\n"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	sub, _ := root.Mkdir("sub", meta(fs.ModeDir|0o755))
	if _, err := sub.Create("data.bin", tree.Bytes(bytes.Repeat([]byte{0xab}, 5000)), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if string(dev.Bytes()[54:62]) != "FAT12   " {
		t.Errorf("fs type = %q, want FAT12", dev.Bytes()[54:62])
	}
}

func TestFAT12Reproducible(t *testing.T) {
	build := func() []byte {
		dev := device.NewMem(2 << 20)
		img, err := New(testDeps(), WithFATBits(12)).Format(dev, image.Params{Label: "TINY"})
		if err != nil {
			t.Fatal(err)
		}
		img.Root().Create("a.txt", tree.Bytes("x"), meta(0o644))
		if err := img.Finalize(); err != nil {
			t.Fatal(err)
		}
		return dev.Bytes()
	}
	if !bytes.Equal(build(), build()) {
		t.Fatal("FAT12 build not reproducible")
	}
}

func TestOpenUnsupported(t *testing.T) {
	if _, err := New(testDeps()).Open(device.NewMem(1 << 20)); err == nil {
		t.Fatal("FAT Open should report it is unsupported")
	}
}

func TestNewNilDeps(t *testing.T) {
	// New must normalise nil Clock/UUID without panicking.
	e := New(image.Deps{})
	if _, err := e.Format(device.NewMem(32<<20), image.Params{}); err != nil {
		t.Fatalf("Format with defaulted deps: %v", err)
	}
}
