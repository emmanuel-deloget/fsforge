package fsforge_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// The trees below are assembled field by field rather than through the editing
// API, because the editing API rejects these names outright. That is the point:
// ExtractToDir is the boundary where an image's bytes become host paths, so it
// has to defend itself even when handed a tree nobody validated.

func hostileTree(name string, n *image.Node) *image.Node {
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	root.Children = []image.Entry{{Name: name, Node: n}}
	return root
}

func regular(body string) *image.Node {
	return &image.Node{
		Inode: tree.Inode{Meta: tree.Meta{Mode: 0o644}, Content: tree.Bytes(body)},
		Nlink: 1,
	}
}

// TestExtractToDirRejectsEscapingNames covers the zip-slip shape: a dirent whose
// name climbs out of the destination once joined onto it.
func TestExtractToDirRejectsEscapingNames(t *testing.T) {
	for _, name := range []string{"..", ".", "", "a/b", "../escape"} {
		t.Run("name="+name, func(t *testing.T) {
			base := t.TempDir()
			dst := filepath.Join(base, "dst")
			canary := filepath.Join(base, "escape")

			err := fsforge.ExtractToDir(hostileTree(name, regular("owned")), dst)
			if err == nil {
				t.Fatalf("ExtractToDir(%q) succeeded, want rejection", name)
			}
			if _, err := os.Lstat(canary); !os.IsNotExist(err) {
				t.Fatalf("wrote outside the destination: %v exists", canary)
			}
		})
	}
}

// TestExtractToDirRejectsEscapingDirNames is the case that actually writes
// outside the destination when the rule is missing. A *directory* named ".."
// resolves to the parent of dst, which MkdirAll happily accepts because it
// already exists; the file underneath then lands next to dst rather than in it.
// (A plain file named ".." fails on its own, since the parent is a directory and
// the open returns EISDIR — which is why that case alone would prove nothing.)
func TestExtractToDirRejectsEscapingDirNames(t *testing.T) {
	base := t.TempDir()
	dst := filepath.Join(base, "dst")

	up := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	up.Children = []image.Entry{{Name: "owned", Node: regular("owned")}}

	if err := fsforge.ExtractToDir(hostileTree("..", up), dst); err == nil {
		t.Fatal("directory named \"..\" accepted, want rejection")
	}
	if _, err := os.Lstat(filepath.Join(base, "owned")); !os.IsNotExist(err) {
		t.Fatalf("escaped the destination: %s exists", filepath.Join(base, "owned"))
	}
}

// TestExtractToDirRejectsNestedEscape checks the same rule one level down, where
// the recursion rather than the top-level loop does the joining.
func TestExtractToDirRejectsNestedEscape(t *testing.T) {
	base := t.TempDir()
	dst := filepath.Join(base, "dst")

	up := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	up.Children = []image.Entry{{Name: "owned", Node: regular("owned")}}
	sub := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	sub.Children = []image.Entry{{Name: "..", Node: up}}

	if err := fsforge.ExtractToDir(hostileTree("sub", sub), dst); err == nil {
		t.Fatal("nested \"..\" accepted, want rejection")
	}
	// Without the rule this lands in dst itself, one level above where it belongs.
	if _, err := os.Lstat(filepath.Join(dst, "owned")); !os.IsNotExist(err) {
		t.Fatal("nested escape wrote outside its directory")
	}
}

// TestExtractToDirDoesNotWriteThroughSymlink covers the other half of the
// surface: a link already sitting in the destination — left by an earlier run,
// or planted by whoever else can write there — must not turn into a write to
// whatever it names.
func TestExtractToDirDoesNotWriteThroughSymlink(t *testing.T) {
	base := t.TempDir()
	dst := filepath.Join(base, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(base, "secret")
	if err := os.WriteFile(secret, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dst, "pwn")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := fsforge.ExtractToDir(hostileTree("pwn", regular("overwritten")), dst)
	if err == nil {
		t.Error("extraction through a pre-existing symlink succeeded, want failure")
	}
	got, rerr := os.ReadFile(secret)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != "original" {
		t.Fatalf("wrote through the symlink: secret = %q", got)
	}
}

// TestExtractToDirAcceptsOrdinaryTrees guards against the validation being too
// eager: names that merely look suspicious are legal and must still extract.
func TestExtractToDirAcceptsOrdinaryTrees(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	root.Children = []image.Entry{
		{Name: "..hidden", Node: regular("a")},
		{Name: "dots...", Node: regular("b")},
		{Name: "lost+found", Node: regular("c")},
	}
	if err := fsforge.ExtractToDir(root, dst); err != nil {
		t.Fatalf("ExtractToDir: %v", err)
	}
	for _, name := range []string{"..hidden", "dots...", "lost+found"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("%s not extracted: %v", name, err)
		}
	}
}
