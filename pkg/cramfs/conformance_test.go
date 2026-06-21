//go:build conformance

package cramfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// trimmed writes the image's real bytes (superblock `size` at offset 4) to path.
func trimmed(t *testing.T, dev *device.Mem, path string) {
	t.Helper()
	b := dev.Bytes()
	size := le.Uint32(b[4:])
	if err := os.WriteFile(path, b[:size], 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCramfs7zExtract extracts a fsforge-written image with 7-Zip's independent
// cramfs reader and checks the directory tree and file contents (including a
// multi-block file). Run: go test -tags conformance ./pkg/cramfs/
func TestCramfs7zExtract(t *testing.T) {
	tmp := t.TempDir()
	img := filepath.Join(tmp, "tree.cramfs")

	dev := device.NewMem(8 << 20)
	im, err := New(testDeps()).Format(dev, image.Params{Label: "cramvol"})
	if err != nil {
		t.Fatal(err)
	}
	root := im.Root()
	etc, _ := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644))
	root.Create("data.bin", tree.Bytes(sampleBytes(10000)), meta(0o644))
	a, _ := root.Mkdir("a", meta(fs.ModeDir|0o755))
	b, _ := a.(image.Dir).Mkdir("b", meta(fs.ModeDir|0o755))
	b.(image.Dir).Create("deep.txt", tree.Bytes("deep\n"), meta(0o644))
	if err := im.Finalize(); err != nil {
		t.Fatal(err)
	}
	trimmed(t, dev, img)

	out := filepath.Join(tmp, "extracted")
	combined, err := conformance.SevenZipExtract(img, out)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("7z unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("7z extract failed: %v\n%s", err, combined)
	}

	if got, _ := os.ReadFile(filepath.Join(out, "etc/hosts")); string(got) != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "data.bin")); string(got) != string(sampleBytes(10000)) {
		t.Errorf("data.bin content mismatch (%d bytes)", len(got))
	}
	if got, _ := os.ReadFile(filepath.Join(out, "a/b/deep.txt")); string(got) != "deep\n" {
		t.Errorf("a/b/deep.txt = %q", got)
	}
}
