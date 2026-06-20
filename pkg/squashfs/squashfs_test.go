package squashfs

import (
	"bytes"
	"encoding/binary"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
	return tree.Meta{Mode: mode, UID: 0, GID: 0, ModTime: time.Unix(1_700_000_000, 0).UTC()}
}

// sampleData is compressible so blocks shrink and exercise the compressed path.
func sampleData(n int) []byte {
	return bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog.\n"), n)
}

func buildSample(t *testing.T, dev device.Device) {
	t.Helper()
	e := New(testDeps())
	img, err := e.Format(dev, image.Params{Label: "fsforge"})
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

	// A file spanning several 128 KiB blocks.
	if _, err := root.Create("data.bin", tree.Bytes(sampleData(20000)), meta(0o600)); err != nil {
		t.Fatalf("Create data.bin: %v", err)
	}
	if err := root.Symlink("link", "etc/hosts", meta(fs.ModeSymlink|0o777)); err != nil {
		t.Fatalf("Symlink: %v", err)
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

func TestSuperblockFields(t *testing.T) {
	dev := device.NewMem(16 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	if got := binary.LittleEndian.Uint32(b[0:]); got != magic {
		t.Fatalf("magic = %#x", got)
	}
	if got := binary.LittleEndian.Uint16(b[28:]); got != versionMajor {
		t.Errorf("major = %d", got)
	}
	bytesUsed := binary.LittleEndian.Uint64(b[40:])
	if bytesUsed == 0 || bytesUsed > uint64(len(b)) {
		t.Errorf("implausible bytesUsed %d", bytesUsed)
	}
}

func TestReproducible(t *testing.T) {
	d1 := device.NewMem(16 << 20)
	d2 := device.NewMem(16 << 20)
	buildSample(t, d1)
	buildSample(t, d2)
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different squashfs images")
	}
}

// TestUnsquashfs validates the image against the reference tool. It is skipped
// when unsquashfs is unavailable.
func TestUnsquashfs(t *testing.T) {
	bin, err := exec.LookPath("unsquashfs")
	if err != nil {
		t.Skip("unsquashfs not installed")
	}

	dev := device.NewMem(16 << 20)
	buildSample(t, dev)

	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "test.squashfs")
	if err := os.WriteFile(imgPath, dev.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "extracted")
	cmd := exec.Command(bin, "-d", out, "-no-xattrs", imgPath)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unsquashfs failed: %v\n%s", err, combined)
	}

	checkFile(t, filepath.Join(out, "etc/hosts"), "127.0.0.1 localhost\n")
	checkFile(t, filepath.Join(out, "data.bin"), string(sampleData(20000)))
	checkFile(t, filepath.Join(out, "a/b/deep.txt"), "deep\n")

	target, err := os.Readlink(filepath.Join(out, "link"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "etc/hosts" {
		t.Errorf("symlink target = %q", target)
	}
}

func checkFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s: content mismatch (got %d bytes, want %d)", path, len(got), len(want))
	}
}
