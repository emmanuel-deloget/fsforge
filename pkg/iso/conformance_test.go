//go:build conformance

package iso

import (
	"errors"
	"io/fs"
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

// TestReadXorrisoISO builds a Rock Ridge ISO with the real xorriso (as mkisofs)
// and reads it back with our parser, checking we recover tool-written names,
// nested files and symlinks. Run: go test -tags conformance ./pkg/iso/
func TestReadXorrisoISO(t *testing.T) {
	parent, err := os.MkdirTemp("", "fsforge-isoread-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(parent)

	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(filepath.Join(src, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "readme.txt"), []byte("hello iso\n"), 0o644)
	os.WriteFile(filepath.Join(src, "etc", "hosts"), []byte("127.0.0.1 localhost\n"), 0o644)
	if err := os.Symlink("etc/hosts", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}

	imgPath := filepath.Join(parent, "out.iso")
	combined, err := conformance.MakeISO(src, imgPath)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("xorriso unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("mkisofs failed: %v\n%s", err, combined)
	}

	f, err := os.Open(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()

	opened, err := New(testDeps()).Open(device.NewFile(f, info.Size()))
	if err != nil {
		t.Fatalf("Open xorriso ISO: %v", err)
	}
	root := opened.(rootNoder).RootNode()

	if got := string(readAll(t, find(root, "readme.txt"))); got != "hello iso\n" {
		t.Errorf("readme.txt = %q", got)
	}
	if got := string(readAll(t, find(find(root, "etc"), "hosts"))); got != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q", got)
	}
	if ln := find(root, "link"); ln == nil || ln.Mode&fs.ModeSymlink == 0 || ln.Link != "etc/hosts" {
		t.Errorf("symlink not recovered from xorriso ISO: %+v", ln)
	}
}
