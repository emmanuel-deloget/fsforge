package manifest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func TestFieldString(t *testing.T) {
	if got := (Type | Content).String(); got != "type,content" {
		t.Errorf("String() = %q, want %q", got, "type,content")
	}
	if got := Field(0).String(); got != "none" {
		t.Errorf("empty String() = %q, want %q", got, "none")
	}
	if got := All.String(); !strings.HasPrefix(got, "type,perm,owner") {
		t.Errorf("All.String() = %q", got)
	}
}

func TestKindNames(t *testing.T) {
	cases := map[fs.FileMode]string{
		fs.ModeDir:                        "dir",
		fs.ModeSymlink:                    "symlink",
		fs.ModeDevice | fs.ModeCharDevice: "chardev",
		fs.ModeDevice:                     "blockdev",
		fs.ModeNamedPipe:                  "fifo",
		fs.ModeSocket:                     "socket",
		0:                                 "file",
	}
	for mode, want := range cases {
		if got := kind(mode); got != want {
			t.Errorf("kind(%v) = %q, want %q", mode, got, want)
		}
	}
}

// TestDiffTypeShowsBitsWhenLabelsMatch covers the case that made a failure read
// as nonsense: ModeCharDevice with and without ModeDevice both print "chardev".
func TestDiffTypeShowsBitsWhenLabelsMatch(t *testing.T) {
	want := Manifest{{Path: "dev", Mode: fs.ModeDevice | fs.ModeCharDevice}}
	got := Manifest{{Path: "dev", Mode: fs.ModeCharDevice}}
	d := Diff(want, got, Options{Fields: Type})
	if len(d) != 1 || !strings.Contains(d[0], "(") {
		t.Fatalf("expected the bits to be spelled out, got %v", d)
	}
}

func TestDiffLinkAndRdev(t *testing.T) {
	want := Manifest{
		{Path: "link", Mode: fs.ModeSymlink, Link: "a"},
		{Path: "dev", Mode: fs.ModeDevice | fs.ModeCharDevice, Rdev: 0x103},
	}
	got := Manifest{
		{Path: "link", Mode: fs.ModeSymlink, Link: "b"},
		{Path: "dev", Mode: fs.ModeDevice | fs.ModeCharDevice, Rdev: 0x104},
	}
	if d := Diff(want, got, Options{Fields: Link}); len(d) != 1 {
		t.Errorf("symlink target change not reported once: %v", d)
	}
	if d := Diff(want, got, Options{Fields: Rdev}); len(d) != 1 {
		t.Errorf("device number change not reported once: %v", d)
	}
}

func TestDiffXattrsAddedAndChanged(t *testing.T) {
	want := Manifest{{Path: "f", Xattrs: map[string][]byte{"user.a": []byte("1")}}}
	got := Manifest{{Path: "f", Xattrs: map[string][]byte{"user.a": []byte("2"), "user.b": []byte("x")}}}
	d := Diff(want, got, Options{Fields: Xattrs})
	if len(d) != 2 {
		t.Fatalf("want one changed and one added, got %v", d)
	}
	joined := strings.Join(d, "\n")
	if !strings.Contains(joined, "added") || !strings.Contains(joined, "want") {
		t.Errorf("both an addition and a change should be named: %v", d)
	}
}

// failingSource is a tree.Source whose reads fail, to exercise the error path
// through the content hash.
type failingSource struct{ size int64 }

func (f failingSource) Size() int64 { return f.size }
func (f failingSource) ReadAt([]byte, int64) (int, error) {
	return 0, errors.New("boom")
}

func TestFromTreePropagatesReadErrors(t *testing.T) {
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}}
	child := &image.Node{
		Inode: tree.Inode{Meta: tree.Meta{Mode: 0o644}, Content: failingSource{size: 8}},
		Nlink: 1,
	}
	if err := root.AddChild("bad", child); err != nil {
		t.Fatal(err)
	}
	_, err := FromTree(root)
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("want an error naming the path, got %v", err)
	}
}

func TestSplitNULHelper(t *testing.T) {
	got := splitNUL([]byte("user.a\x00user.b\x00\x00"))
	if len(got) != 2 || got[0] != "user.a" || got[1] != "user.b" {
		t.Errorf("splitNUL = %q, want [user.a user.b]", got)
	}
	if len(splitNUL(nil)) != 0 {
		t.Error("splitNUL(nil) should be empty")
	}
}

// TestFromDirReadsXattrs needs a filesystem that carries user.* attributes;
// tmpfs does not, so the test skips rather than failing where it cannot run.
func TestFromDirReadsXattrs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setxattr(p, "user.fsforge", []byte("value")); err != nil {
		t.Skipf("xattrs unavailable here: %v", err)
	}
	m, err := FromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := index(m)["f"].Xattrs
	if string(got["user.fsforge"]) != "value" {
		t.Errorf("xattr not read back: %q", got)
	}
}
