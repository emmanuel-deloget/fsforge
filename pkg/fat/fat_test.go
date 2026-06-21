package fat

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
		UUID:  image.FixedUUID{V: [16]byte{1, 2, 3, 4}},
	}
}

func meta(mode fs.FileMode) tree.Meta {
	return tree.Meta{Mode: mode, ModTime: time.Unix(1_700_000_000, 0).UTC()}
}

func buildSample(t *testing.T, dev device.Device) {
	buildSampleWith(t, New(testDeps(), WithFATBits(32)), dev)
}

func buildSampleWith(t *testing.T, e *FAT, dev device.Device) {
	t.Helper()
	img, err := e.Format(dev, image.Params{Label: "FSFORGE"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()
	efi, err := root.Mkdir("EFI", meta(fs.ModeDir|0o755))
	if err != nil {
		t.Fatal(err)
	}
	boot, _ := efi.Mkdir("BOOT", meta(fs.ModeDir|0o755))
	if _, err := boot.Create("BOOTX64.EFI", tree.Bytes(bytes.Repeat([]byte{0x90}, 100000)), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	// Long name forcing LFN entries.
	if _, err := root.Create("a long readme file.txt", tree.Bytes("hello fat\n"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Create("config.cfg", tree.Bytes("k=v\n"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func TestBootSectorSane(t *testing.T) {
	dev := device.NewMem(64 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	if b[510] != 0x55 || b[511] != 0xAA {
		t.Errorf("missing boot signature")
	}
	if string(b[82:90]) != "FAT32   " {
		t.Errorf("fs type = %q", b[82:90])
	}
	if le.Uint16(b[11:]) != sectorSize {
		t.Errorf("bytesPerSec = %d", le.Uint16(b[11:]))
	}
	if le.Uint32(b[44:]) != rootCluster {
		t.Errorf("rootClus = %d", le.Uint32(b[44:]))
	}
	// FSInfo signatures.
	if le.Uint32(b[sectorSize:]) != 0x41615252 {
		t.Errorf("FSInfo lead sig wrong")
	}
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(64 << 20)
	d2 := device.NewMem(64 << 20)
	buildSample(t, d1)
	buildSample(t, d2)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different FAT images")
	}
}

func TestSymlinkRejected(t *testing.T) {
	dev := device.NewMem(64 << 20)
	img, err := New(testDeps()).Format(dev, image.Params{})
	if err != nil {
		t.Fatal(err)
	}
	if err := img.Root().Symlink("link", "target", meta(fs.ModeSymlink|0o777)); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err == nil {
		t.Fatal("expected error finalizing FAT with a symlink")
	}
}

func TestTooSmall(t *testing.T) {
	dev := device.NewMem(16 << 10) // 16 KiB: too small for any FAT layout
	if _, err := New(testDeps()).Format(dev, image.Params{}); err == nil {
		t.Fatal("expected error for too-small device")
	}
}

func TestFATTypeSelection(t *testing.T) {
	cases := []struct {
		size int64
		bits int
	}{
		{2 << 20, 12},   // ~2 MiB -> FAT12
		{32 << 20, 16},  // 32 MiB -> FAT16
		{600 << 20, 32}, // 600 MiB -> FAT32
	}
	for _, c := range cases {
		g, err := computeGeometry(c.size, 0)
		if err != nil {
			t.Fatalf("size %d: %v", c.size, err)
		}
		if g.fatBits != c.bits {
			t.Errorf("size %d: fatBits = %d, want %d", c.size, g.fatBits, c.bits)
		}
	}
}

func TestFAT16Reproducible(t *testing.T) {
	d1 := device.NewMem(32 << 20)
	d2 := device.NewMem(32 << 20)
	buildSampleWith(t, New(testDeps(), WithFATBits(16)), d1)
	buildSampleWith(t, New(testDeps(), WithFATBits(16)), d2)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different FAT16 images")
	}
	if string(d1.Bytes()[54:62]) != "FAT16   " {
		t.Errorf("fs type = %q", d1.Bytes()[54:62])
	}
}
