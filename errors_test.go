package fsforge_test

import (
	"os"
	"path/filepath"
	"testing"

	fsforge "github.com/emmanuel-deloget/fsforge"
)

func TestBuildFromDirMissingSource(t *testing.T) {
	err := fsforge.New("ext2").Size("16M").
		BuildFromDir(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "o.img"))
	if err == nil {
		t.Fatal("building from a missing directory should fail")
	}
}

func TestConvertMissingImageSource(t *testing.T) {
	err := fsforge.Convert(
		fsforge.Location{Kind: "ext2", Path: filepath.Join(t.TempDir(), "absent.img")},
		fsforge.Location{Kind: "dir", Path: t.TempDir()},
		fsforge.Options{},
	)
	if err == nil {
		t.Fatal("converting from a missing image should fail")
	}
}

func TestConvertCorruptSquashfsSource(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.sqfs")
	if err := os.WriteFile(bad, []byte("not a squashfs image at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := fsforge.Convert(
		fsforge.Location{Kind: "squashfs", Path: bad},
		fsforge.Location{Kind: "dir", Path: t.TempDir()},
		fsforge.Options{},
	)
	if err == nil {
		t.Fatal("converting from a corrupt squashfs should fail")
	}
}

func TestBuildInvalidBlockSize(t *testing.T) {
	// squashfs rejects a non-power-of-two block size at Format time, surfacing
	// through the Builder's create pipeline.
	err := fsforge.New("squashfs").BlockSize(1000).
		BuildFromDir(sampleTree(t), filepath.Join(t.TempDir(), "x.sqfs"))
	if err == nil {
		t.Fatal("an invalid squashfs block size should fail the build")
	}
}
