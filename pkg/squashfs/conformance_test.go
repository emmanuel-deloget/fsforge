//go:build conformance

package squashfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/internal/manifest"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// TestXattrConformance checks that the attribute tables mean to squashfs-tools
// what they mean here. Reading them back with fsforge only proves the writer
// and the reader agree with each other.
//
// Only user.* attributes are asserted on the extracted files: restoring
// security.* or trusted.* needs privileges this test does not have, and their
// absence on disk would say nothing about the image. That the image *carries*
// them is covered by the in-process round trip.
func TestXattrConformance(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "xattr.sqsh")

	want := map[string][]byte{
		"user.short":   []byte("v"),
		"user.long":    []byte(strings.Repeat("x", 300)),
		"user.empty":   nil,
		"user.comment": []byte("système de fichiers"),
	}
	// A second node with the same set must share one id record, and a third with
	// a different set must get its own.
	other := map[string][]byte{"user.other": []byte("2")}

	dev := device.NewMem(16 << 20)
	img, err := New(testDeps()).Format(dev, image.Params{})
	if err != nil {
		t.Fatal(err)
	}
	root := img.Root()
	m := meta(0o644)
	m.Xattrs = want
	for _, name := range []string{"a", "b"} {
		if _, err := root.Create(name, tree.Bytes("payload\n"), m); err != nil {
			t.Fatal(err)
		}
	}
	om := meta(0o644)
	om.Xattrs = other
	if _, err := root.Create("c", tree.Bytes("c\n"), om); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Create("plain", tree.Bytes("p\n"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	dm := meta(os.ModeDir | 0o755)
	dm.Xattrs = want
	if _, err := root.Mkdir("dir", dm); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgPath, dev.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(tmp, "extracted")
	out, err := conformance.UnsquashfsXattrs(imgPath, dest)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("unsquashfs unavailable")
	}
	if err != nil {
		t.Fatalf("unsquashfs: %v\n%s", err, out)
	}

	got, err := manifest.FromDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]manifest.Entry{}
	for _, e := range got {
		byPath[e.Path] = e
	}
	if len(byPath["a"].Xattrs) == 0 {
		t.Skipf("the extraction filesystem does not carry user attributes")
	}
	for _, path := range []string{"a", "b", "dir"} {
		for k, v := range want {
			if g := byPath[path].Xattrs[k]; string(g) != string(v) {
				t.Errorf("%s: %s = %q, want %q", path, k, g, v)
			}
		}
	}
	if g := byPath["c"].Xattrs["user.other"]; string(g) != "2" {
		t.Errorf("c: user.other = %q, want %q", g, "2")
	}
	if n := len(byPath["plain"].Xattrs); n != 0 {
		t.Errorf("plain: got %d attributes, want none", n)
	}
}
