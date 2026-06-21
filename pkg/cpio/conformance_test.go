//go:build conformance

package cpio

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
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// TestCpioExtract builds an archive and unpacks it with the real GNU cpio,
// checking file contents, a symlink and a hard link survived.
// Run: go test -tags conformance ./pkg/cpio/
func TestCpioExtract(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "test.cpio")

	// A device-free sample: unprivileged extraction cannot mknod, and device
	// inodes are already covered by the unit and read-back tests.
	dev := device.NewMem(8 << 20)
	img, _ := New(testDeps()).Format(dev, image.Params{})
	root := img.Root()
	etc, _ := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644))
	root.Create("data.bin", tree.Bytes(sampleData(500)), meta(0o644))
	root.Symlink("link", "etc/hosts", meta(fs.ModeSymlink|0o777))
	a, _ := root.Mkdir("a", meta(fs.ModeDir|0o755))
	b, _ := a.(image.Dir).Mkdir("b", meta(fs.ModeDir|0o755))
	b.(image.Dir).Create("deep.txt", tree.Bytes("deep\n"), meta(0o644))
	orig, _ := root.Create("orig.txt", tree.Bytes("shared body\n"), meta(0o644))
	root.Link("hard.txt", orig)
	if err := img.Finalize(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgPath, dev.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "extracted")
	combined, err := conformance.CpioExtract(imgPath, out)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("cpio unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("cpio -i failed: %v\n%s", err, combined)
	}

	if got, _ := os.ReadFile(filepath.Join(out, "etc/hosts")); string(got) != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "data.bin")); string(got) != string(sampleData(500)) {
		t.Errorf("data.bin content mismatch (%d bytes)", len(got))
	}
	if got, _ := os.ReadFile(filepath.Join(out, "a/b/deep.txt")); string(got) != "deep\n" {
		t.Errorf("a/b/deep.txt = %q", got)
	}
	if target, err := os.Readlink(filepath.Join(out, "link")); err != nil || target != "etc/hosts" {
		t.Errorf("symlink = %q (%v)", target, err)
	}

	// orig.txt and hard.txt must be the same inode after extraction.
	oi, err1 := os.Stat(filepath.Join(out, "orig.txt"))
	hi, err2 := os.Stat(filepath.Join(out, "hard.txt"))
	if err1 != nil || err2 != nil {
		t.Fatalf("hard-link stat: %v %v", err1, err2)
	}
	if !os.SameFile(oi, hi) {
		t.Errorf("orig.txt and hard.txt are not the same inode")
	}
	if got, _ := os.ReadFile(filepath.Join(out, "hard.txt")); string(got) != "shared body\n" {
		t.Errorf("hard.txt content = %q", got)
	}
}

// TestReadGnuCpio archives a tree with the real GNU cpio (newc) and reads it
// back with our parser.
// Run: go test -tags conformance ./pkg/cpio/
func TestReadGnuCpio(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(filepath.Join(src, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "readme.txt"), []byte("hello cpio\n"), 0o644)
	os.WriteFile(filepath.Join(src, "etc", "hosts"), []byte("127.0.0.1 localhost\n"), 0o644)
	os.WriteFile(filepath.Join(src, "big.bin"), sampleData(500), 0o644)
	if err := os.Symlink("etc/hosts", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	// A hard link the tool will encode with a shared inode.
	if err := os.Link(filepath.Join(src, "readme.txt"), filepath.Join(src, "readme.hard")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 150; i++ {
		os.WriteFile(filepath.Join(src, fmt.Sprintf("f-%03d", i)), []byte(fmt.Sprintf("f-%03d", i)), 0o644)
	}

	imgPath := filepath.Join(parent, "out.cpio")
	combined, err := conformance.MakeCpio(src, imgPath)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("cpio unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("cpio -o failed: %v\n%s", err, combined)
	}

	f, err := os.Open(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()

	opened, err := New(testDeps()).Open(device.NewFile(f, info.Size()))
	if err != nil {
		t.Fatalf("Open GNU cpio archive: %v", err)
	}
	root := opened.(interface{ RootNode() *image.Node }).RootNode()

	if got := string(readAll(t, find(root, "readme.txt").Content)); got != "hello cpio\n" {
		t.Errorf("readme.txt = %q", got)
	}
	if got := string(readAll(t, find(find(root, "etc"), "hosts").Content)); got != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q", got)
	}
	if got := readAll(t, find(root, "big.bin").Content); string(got) != string(sampleData(500)) {
		t.Errorf("big.bin mismatch (%d bytes)", len(got))
	}
	if ln := find(root, "link"); ln == nil || ln.Mode&fs.ModeSymlink == 0 || ln.Link != "etc/hosts" {
		t.Errorf("symlink not recovered: %+v", ln)
	}
	if a, b := find(root, "readme.txt"), find(root, "readme.hard"); a == nil || a != b {
		t.Errorf("hard link not folded onto a shared node (a=%p b=%p)", a, b)
	}
	for i := 0; i < 150; i++ {
		name := fmt.Sprintf("f-%03d", i)
		if c := find(root, name); c == nil || string(readAll(t, c.Content)) != name {
			t.Fatalf("%s lost", name)
		}
	}
}
