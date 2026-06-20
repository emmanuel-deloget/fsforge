package iso

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

func buildSample(t *testing.T, dev device.Device) {
	t.Helper()
	img, err := New(testDeps()).Format(dev, image.Params{Label: "FSFORGE"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()
	etc, err := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Create("a-long-readme-file.txt", tree.Bytes(bytes.Repeat([]byte("iso\n"), 1000)), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("link", "etc/hosts", meta(fs.ModeSymlink|0o777)); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func TestPVDSane(t *testing.T) {
	dev := device.NewMem(16 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	pvd := b[pvdSector*sectorSize:]
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" {
		t.Fatalf("bad PVD descriptor header")
	}
	term := b[(pvdSector+1)*sectorSize:]
	if term[0] != 255 || string(term[1:6]) != "CD001" {
		t.Errorf("bad terminator")
	}
	// logical block size both-endian = 2048
	if le.Uint16(pvd[128:]) != sectorSize {
		t.Errorf("block size = %d", le.Uint16(pvd[128:]))
	}
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(16 << 20)
	d2 := device.NewMem(16 << 20)
	buildSample(t, d1)
	buildSample(t, d2)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different ISO images")
	}
}

func TestRockRidgeEntriesPresent(t *testing.T) {
	dev := device.NewMem(16 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	for _, sig := range []string{"SP", "ER", "PX", "NM", "TF", "SL"} {
		if !bytes.Contains(b, []byte(sig)) {
			t.Errorf("missing Rock Ridge entry %q", sig)
		}
	}
}
