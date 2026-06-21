//go:build conformance

package udf

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// trimmed writes the sample image's used blocks (up to the second anchor) to
// path: the Partition Descriptor at block 22 records the partition length.
func trimmed(t *testing.T, dev *device.Mem, path string) {
	t.Helper()
	b := dev.Bytes()
	start := le.Uint32(b[22*blockSize+188:])
	length := le.Uint32(b[22*blockSize+192:])
	if err := os.WriteFile(path, b[:int(start+length+1)*blockSize], 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestUdfInfo validates a fsforge-written image with udfinfo (udftools), which
// parses every volume descriptor. Run: go test -tags conformance ./pkg/udf/
func TestUdfInfo(t *testing.T) {
	tmp := t.TempDir()
	img := filepath.Join(tmp, "test.udf")
	dev := device.NewMem(8 << 20)
	buildSample(t, dev)
	trimmed(t, dev, img)

	out, err := conformance.UdfInfo(img)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("udfinfo unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("udfinfo failed: %v\n%s", err, out)
	}
	for _, want := range []string{"label=FSFORGE", "udfrev=2.01", "integrity=closed", "numdirs=4", "numfiles=6"} {
		if !strings.Contains(out, want) {
			t.Errorf("udfinfo output missing %q:\n%s", want, out)
		}
	}
}

// TestUdf7zExtract extracts a fsforge-written image with 7-Zip's independent UDF
// reader and checks file contents and the directory tree. The sample avoids
// symlinks and device nodes, which 7-Zip cannot represent.
func TestUdf7zExtract(t *testing.T) {
	tmp := t.TempDir()
	img := filepath.Join(tmp, "tree.udf")

	dev := device.NewMem(8 << 20)
	e := New(testDeps())
	im, err := e.Format(dev, image.Params{Label: "FSFORGE"})
	if err != nil {
		t.Fatal(err)
	}
	root := im.Root()
	etc, _ := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644))
	root.Create("data.bin", tree.Bytes(sampleBytes(5000)), meta(0o644))
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
	if got, _ := os.ReadFile(filepath.Join(out, "data.bin")); string(got) != string(sampleBytes(5000)) {
		t.Errorf("data.bin content mismatch (%d bytes)", len(got))
	}
	if got, _ := os.ReadFile(filepath.Join(out, "a/b/deep.txt")); string(got) != "deep\n" {
		t.Errorf("a/b/deep.txt = %q", got)
	}
}
