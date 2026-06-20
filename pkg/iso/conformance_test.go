//go:build conformance

package iso

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// TestXorrisoExtract builds an ISO and extracts it with the real xorriso,
// checking Rock Ridge names, file contents and a symlink survived.
// Run: go test -tags conformance ./pkg/iso/
func TestXorrisoExtract(t *testing.T) {
	dir, err := os.MkdirTemp("", "fsforge-iso-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	imgPath := filepath.Join(dir, "test.iso")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	const size = 16 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	buildSample(t, device.NewFile(f, size))
	f.Close()

	out := filepath.Join(dir, "extracted")
	combined, err := conformance.XorrisoExtract(imgPath, out)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("xorriso unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("xorriso failed: %v\n%s", err, combined)
	}

	if got, _ := os.ReadFile(filepath.Join(out, "etc/hosts")); string(got) != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q", got)
	}
	if fi, err := os.Stat(filepath.Join(out, "a-long-readme-file.txt")); err != nil || fi.Size() != 4000 {
		t.Errorf("long file: %v size=%v", err, fi)
	}
	if target, err := os.Readlink(filepath.Join(out, "link")); err != nil || target != "etc/hosts" {
		t.Errorf("symlink = %q (%v)", target, err)
	}
}
