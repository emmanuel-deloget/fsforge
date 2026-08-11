package manifest

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func deps() image.Deps {
	return image.Deps{Clock: image.FixedClock{T: time.Unix(1_600_000_000, 0).UTC()}}
}

// build makes a small tree covering the node kinds the manifest models.
func build(t *testing.T) *image.Mem {
	t.Helper()
	mem := image.NewMem(deps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	root := mem.Root()
	m := func(mode fs.FileMode) tree.Meta {
		return tree.Meta{Mode: mode, UID: 1000, GID: 2000,
			ModTime: time.Unix(1_600_000_000, 0).UTC()}
	}
	h, err := root.Create("file", tree.Bytes("payload"), m(0o644))
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Link("hard", h); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("link", "file", m(fs.ModeSymlink|0o777)); err != nil {
		t.Fatal(err)
	}
	sub, err := root.Mkdir("dir", m(fs.ModeDir|0o755))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Create("nested", tree.Bytes(""), m(0o600)); err != nil {
		t.Fatal(err)
	}
	return mem
}

func TestFromTreeShape(t *testing.T) {
	m, err := FromTree(build(t).RootNode())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range m {
		paths = append(paths, e.Path)
	}
	want := "dir,dir/nested,file,hard,link"
	if got := strings.Join(paths, ","); got != want {
		t.Errorf("paths = %q, want %q (sorted, root excluded)", got, want)
	}
}

// TestLinkGroups is the property the link count alone cannot express: the two
// names for one inode must carry the same group, and nothing else may.
func TestLinkGroups(t *testing.T) {
	m, err := FromTree(build(t).RootNode())
	if err != nil {
		t.Fatal(err)
	}
	byPath := index(m)
	if g := byPath["file"].LinkGroup; g == 0 || g != byPath["hard"].LinkGroup {
		t.Errorf("file and hard should share a group, got %d and %d",
			g, byPath["hard"].LinkGroup)
	}
	for _, p := range []string{"dir", "dir/nested", "link"} {
		if g := byPath[p].LinkGroup; g != 0 {
			t.Errorf("%s is unshared but carries group %d", p, g)
		}
	}
}

func TestDiffDetectsEachField(t *testing.T) {
	base, err := FromTree(build(t).RootNode())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		field  Field
		mutate func(*Entry)
	}{
		{"perm", Perm, func(e *Entry) { e.Mode = e.Mode&^0o777 | 0o600 }},
		{"owner", Owner, func(e *Entry) { e.UID = 7 }},
		{"mtime", MTime, func(e *Entry) { e.MTime = e.MTime.Add(time.Hour) }},
		{"size", Size, func(e *Entry) { e.Size = 99 }},
		{"content", Content, func(e *Entry) { e.Digest = "deadbeef" }},
		{"nlink", Nlink, func(e *Entry) { e.Nlink = 42 }},
		{"links", Links, func(e *Entry) { e.LinkGroup = 0 }},
		{"xattrs", Xattrs, func(e *Entry) { e.Xattrs = map[string][]byte{"user.x": []byte("v")} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := append(Manifest(nil), base...)
			for i := range got {
				if got[i].Path == "file" {
					tc.mutate(&got[i])
				}
			}
			if d := Diff(base, got, Options{Fields: tc.field}); len(d) == 0 {
				t.Errorf("Diff missed a %s change", tc.name)
			}
			// The same change must be invisible when the field is not selected.
			if d := Diff(base, got, Options{Fields: All &^ tc.field}); len(d) != 0 {
				t.Errorf("Diff reported %s while it was deselected: %v", tc.name, d)
			}
		})
	}
}

func TestDiffMintedAndMissing(t *testing.T) {
	base, err := FromTree(build(t).RootNode())
	if err != nil {
		t.Fatal(err)
	}
	extra := append(Manifest(nil), base...)
	extra = append(extra, Entry{Path: "lost+found", Mode: fs.ModeDir | 0o700})

	if d := Diff(base, extra, Options{}); len(d) != 1 {
		t.Errorf("an unexpected path should be reported once, got %v", d)
	}
	if d := Diff(base, extra, Options{Minted: []string{"lost+found"}}); len(d) != 0 {
		t.Errorf("a minted path should be tolerated, got %v", d)
	}
	if d := Diff(base, base[:len(base)-1], Options{}); len(d) != 1 {
		t.Errorf("a dropped path should be reported once, got %v", d)
	}
}

// TestMTimeGrain covers the DOS-timestamp case: a one-second difference is a
// real loss at second resolution and noise at exFAT's two-second resolution.
func TestMTimeGrain(t *testing.T) {
	base, err := FromTree(build(t).RootNode())
	if err != nil {
		t.Fatal(err)
	}
	got := append(Manifest(nil), base...)
	for i := range got {
		got[i].MTime = got[i].MTime.Add(-time.Second)
	}
	if d := Diff(base, got, Options{Fields: MTime}); len(d) == 0 {
		t.Error("a one-second drift should show at default resolution")
	}
	if d := Diff(base, got, Options{Fields: MTime, MTimeGrain: 2 * time.Second}); len(d) != 0 {
		t.Errorf("a one-second drift should vanish at two-second grain: %v", d)
	}
}

// TestFromDirMatchesTree checks the two sources agree, which is what makes a
// comparison against an external tool's extraction meaningful at all.
func TestFromDirMatchesTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "file"), filepath.Join(dir, "hard")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := os.Symlink("file", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dir", "nested"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	fromDir, err := FromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fromTree, err := FromTree(build(t).RootNode())
	if err != nil {
		t.Fatal(err)
	}
	// Owner and mtime come from the host here, so only the structural fields are
	// comparable; that is exactly the subset a conformance test relies on.
	d := Diff(fromTree, fromDir, Options{Fields: Type | Perm | Size | Content | Link | Links})
	if len(d) != 0 {
		t.Errorf("host directory and tree disagree:\n%s", strings.Join(d, "\n"))
	}
}
