//go:build conformance

package romfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// TestReadGenromfs builds an image with the real genromfs and reads it back with
// our parser, checking file contents, nested directories and a symlink — an
// independent check of our romfs format understanding. romfs is deprecated and
// compiled out of current kernels (so a loopback mount is unavailable) and 7-Zip
// does not read it, which leaves genromfs as the reference implementation.
// Run: go test -tags conformance ./pkg/romfs/
func TestReadGenromfs(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(filepath.Join(src, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "readme.txt"), []byte("hello romfs\n"), 0o644)
	os.WriteFile(filepath.Join(src, "etc", "hosts"), []byte("127.0.0.1 localhost\n"), 0o644)
	os.MkdirAll(filepath.Join(src, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(src, "a", "b", "deep.txt"), []byte("deep\n"), 0o644)
	if err := os.Symlink("etc/hosts", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		os.WriteFile(filepath.Join(src, fmt.Sprintf("f-%02d", i)), []byte(fmt.Sprintf("f-%02d", i)), 0o644)
	}

	img := filepath.Join(parent, "out.romfs")
	combined, err := conformance.MakeRomfs(src, img)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("genromfs unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("genromfs failed: %v\n%s", err, combined)
	}

	f, err := os.Open(img)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()

	opened, err := New(testDeps()).Open(device.NewFile(f, info.Size()))
	if err != nil {
		t.Fatalf("Open genromfs image: %v", err)
	}
	root := opened.(interface{ RootNode() *image.Node }).RootNode()

	if got := string(readAll(t, find(root, "readme.txt").Content)); got != "hello romfs\n" {
		t.Errorf("readme.txt = %q", got)
	}
	if got := string(readAll(t, find(find(root, "etc"), "hosts").Content)); got != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q", got)
	}
	if got := string(readAll(t, find(find(find(root, "a"), "b"), "deep.txt").Content)); got != "deep\n" {
		t.Errorf("a/b/deep.txt = %q", got)
	}
	if ln := find(root, "link"); ln == nil || ln.Mode&fs.ModeSymlink == 0 || ln.Link != "etc/hosts" {
		t.Errorf("symlink not recovered from genromfs image: %+v", ln)
	}
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("f-%02d", i)
		if c := find(root, name); c == nil || string(readAll(t, c.Content)) != name {
			t.Fatalf("%s lost", name)
		}
	}
}
