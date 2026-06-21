package exfat

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
	t.Helper()
	img, err := New(testDeps()).Format(dev, image.Params{Label: "FSFORGE"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()
	sub, err := root.Mkdir("subdir", meta(fs.ModeDir|0o755))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Create("file in subdir.txt", tree.Bytes(bytes.Repeat([]byte("X"), 200000)), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Create("a long file name here.dat", tree.Bytes("hello exfat\n"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Create("short.txt", tree.Bytes("hi\n"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func TestBootSane(t *testing.T) {
	dev := device.NewMem(64 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	if string(b[3:11]) != "EXFAT   " {
		t.Fatalf("FS name = %q", b[3:11])
	}
	if b[510] != 0x55 || b[511] != 0xAA {
		t.Errorf("missing boot signature")
	}
	if le.Uint16(b[104:]) != 0x0100 {
		t.Errorf("fs revision = %#x", le.Uint16(b[104:]))
	}
	if b[108] != bytesPerSectorShift {
		t.Errorf("bytesPerSectorShift = %d", b[108])
	}
	// Backup boot region must duplicate the main one.
	if !bytes.Equal(b[:bytesPerSector], b[12*bytesPerSector:13*bytesPerSector]) {
		t.Errorf("backup boot sector differs")
	}
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(64 << 20)
	d2 := device.NewMem(64 << 20)
	buildSample(t, d1)
	buildSample(t, d2)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different exFAT images")
	}
}

func TestNameHash(t *testing.T) {
	// Up-casing must fold ASCII so "abc" and "ABC" hash equally.
	if nameHash(nameUTF16("abc")) != nameHash(nameUTF16("ABC")) {
		t.Errorf("name hash not case-insensitive")
	}
}

func TestSymlinkRejected(t *testing.T) {
	dev := device.NewMem(64 << 20)
	img, err := New(testDeps()).Format(dev, image.Params{})
	if err != nil {
		t.Fatal(err)
	}
	if err := img.Root().Symlink("l", "t", meta(fs.ModeSymlink|0o777)); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err == nil {
		t.Fatal("expected error for symlink in exFAT")
	}
}
