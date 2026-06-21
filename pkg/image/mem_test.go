package image

import (
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func newImage() *Mem {
	return NewMem(Deps{Clock: FixedClock{T: time.Unix(42, 0).UTC()}, UUID: FixedUUID{}},
		tree.Meta{Mode: fs.ModeDir | 0o755})
}

func TestNewMemNormalisesNilDeps(t *testing.T) {
	// nil Clock/UUID must be filled in rather than panic.
	m := NewMem(Deps{}, tree.Meta{})
	if m.Deps().Clock == nil || m.Deps().UUID == nil {
		t.Fatal("NewMem left nil Clock/UUID")
	}
	if !m.RootNode().IsDir() {
		t.Fatal("root is not a directory")
	}
}

func TestMkdirAndLookup(t *testing.T) {
	root := newImage().Root()
	sub, err := root.Mkdir("etc", tree.Meta{Mode: 0o755})
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := sub.Create("hosts", tree.Bytes("127.0.0.1"), tree.Meta{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	de, err := root.Lookup("etc")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !de.Inode.Mode.IsDir() {
		t.Fatal("etc is not a directory")
	}
	if _, err := root.Lookup("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lookup missing = %v, want ErrNotExist", err)
	}
}

func TestModTimeResolvedFromClock(t *testing.T) {
	m := newImage()
	if _, err := m.Root().Mkdir("x", tree.Meta{}); err != nil {
		t.Fatal(err)
	}
	de, _ := m.Root().Lookup("x")
	if !de.Inode.ModTime.Equal(time.Unix(42, 0).UTC()) {
		t.Fatalf("ModTime = %v, want clock value", de.Inode.ModTime)
	}
}

func TestDuplicateNameRejected(t *testing.T) {
	root := newImage().Root()
	root.Mkdir("dup", tree.Meta{})
	if _, err := root.Mkdir("dup", tree.Meta{}); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("duplicate Mkdir = %v, want ErrExist", err)
	}
}

func TestInvalidNames(t *testing.T) {
	root := newImage().Root()
	for _, name := range []string{"", ".", "..", "a/b"} {
		if _, err := root.Mkdir(name, tree.Meta{}); err == nil {
			t.Fatalf("Mkdir(%q) should fail", name)
		}
	}
}

func TestSymlinkAndMknod(t *testing.T) {
	root := newImage().Root()
	if err := root.Symlink("link", "/target", tree.Meta{}); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	de, _ := root.Lookup("link")
	if de.Inode.Mode&fs.ModeSymlink == 0 || de.Inode.Link != "/target" {
		t.Fatalf("symlink not stored: mode=%v link=%q", de.Inode.Mode, de.Inode.Link)
	}
	if err := root.Mknod("null", 0x0103, tree.Meta{Mode: fs.ModeCharDevice}); err != nil {
		t.Fatalf("Mknod: %v", err)
	}
	de, _ = root.Lookup("null")
	if de.Inode.Mode&fs.ModeCharDevice == 0 || de.Inode.Rdev != 0x0103 {
		t.Fatalf("device not stored: mode=%v rdev=%#x", de.Inode.Mode, de.Inode.Rdev)
	}
}

func TestHardLinkSharesInodeAndNlink(t *testing.T) {
	root := newImage().Root()
	h, err := root.Create("a", tree.Bytes("x"), tree.Meta{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := root.Link("b", h); err != nil {
		t.Fatalf("Link: %v", err)
	}
	da, _ := root.Lookup("a")
	db, _ := root.Lookup("b")
	if da.Inode != db.Inode {
		t.Fatal("hard link does not share the inode")
	}
	// Link onto a non-file handle / bad target must be rejected.
	if err := root.Link("c", badFile{}); !errors.Is(err, errBadLink) {
		t.Fatalf("Link with bad target = %v, want errBadLink", err)
	}
	if err := root.Link("a", h); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Link onto existing name = %v, want ErrExist", err)
	}
}

type badFile struct{}

func (badFile) inode() *tree.Inode { return nil }

func TestRemove(t *testing.T) {
	root := newImage().Root()
	sub, _ := root.Mkdir("d", tree.Meta{})
	sub.Create("f", tree.Bytes("x"), tree.Meta{})
	if err := root.Remove("d"); !errors.Is(err, errNotEmpty) {
		t.Fatalf("Remove non-empty dir = %v, want errNotEmpty", err)
	}
	if err := sub.Remove("f"); err != nil {
		t.Fatalf("Remove file: %v", err)
	}
	if err := root.Remove("d"); err != nil {
		t.Fatalf("Remove empty dir: %v", err)
	}
	if err := root.Remove("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Remove missing = %v, want ErrNotExist", err)
	}
}

func TestRange(t *testing.T) {
	root := newImage().Root()
	root.Mkdir("a", tree.Meta{})
	root.Mkdir("b", tree.Meta{})
	var names []string
	err := root.Range(func(d tree.Dirent) error {
		names = append(names, d.Name)
		return nil
	})
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("Range visited %v, want 2 entries", names)
	}
	// A returned error must propagate and stop iteration.
	sentinel := errors.New("stop")
	if err := root.Range(func(tree.Dirent) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("Range error = %v, want sentinel", err)
	}
}

func TestSubDir(t *testing.T) {
	m := newImage()
	root := m.Root().(*dirHandle)
	root.Mkdir("sub", tree.Meta{})
	root.Create("file", tree.Bytes("x"), tree.Meta{})
	if _, err := root.SubDir("sub"); err != nil {
		t.Fatalf("SubDir: %v", err)
	}
	if _, err := root.SubDir("file"); !errors.Is(err, errNotDir) {
		t.Fatalf("SubDir on file = %v, want errNotDir", err)
	}
	if _, err := root.SubDir("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("SubDir missing = %v, want ErrNotExist", err)
	}
}

func TestAdopt(t *testing.T) {
	orig := newImage()
	orig.Root().Mkdir("keep", tree.Meta{})
	adopted := Adopt(Deps{}, orig.RootNode())
	if _, err := adopted.Root().Lookup("keep"); err != nil {
		t.Fatalf("Adopt lost children: %v", err)
	}
}
