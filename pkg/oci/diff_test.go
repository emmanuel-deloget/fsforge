package oci

import (
	"archive/tar"
	"io"
	"io/fs"
	"sort"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// layerEntryNames lists the tar entry names in a (non-gzipped) layer blob.
func layerEntryNames(t *testing.T, l *Layout, digest string) []string {
	t.Helper()
	rc, err := l.BlobReader(digest)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var names []string
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	sort.Strings(names)
	return names
}

func TestAddLayerDiff(t *testing.T) {
	dir := t.TempDir()
	l, err := CreateLayout(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Base image.
	base := image.NewMem(testDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	etc, _ := base.Root().Mkdir("etc", meta(fs.ModeDir|0o755))
	etc.Create("hosts", tree.Bytes("old\n"), meta(0o644))
	base.Root().Create("keep.txt", tree.Bytes("keep\n"), meta(0o644))
	base.Root().Create("delete-me.txt", tree.Bytes("bye\n"), meta(0o644))
	od, _ := base.Root().Mkdir("olddir", meta(fs.ModeDir|0o755))
	od.Create("inner", tree.Bytes("x"), meta(0o644))
	if _, err := Build(l, base, BuildOptions{Ref: "img:1", Gzip: false}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Desired end state: hosts changed, keep.txt unchanged, new file added,
	// delete-me.txt and olddir removed.
	next := image.NewMem(testDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	netc, _ := next.Root().Mkdir("etc", meta(fs.ModeDir|0o755))
	netc.Create("hosts", tree.Bytes("new\n"), meta(0o644))
	next.Root().Create("keep.txt", tree.Bytes("keep\n"), meta(0o644)) // identical
	next.Root().Create("added.txt", tree.Bytes("added\n"), meta(0o644))

	manDesc, err := AddLayerDiff(l, "img:1", next, BuildOptions{Ref: "img:1", Gzip: false})
	if err != nil {
		t.Fatalf("AddLayerDiff: %v", err)
	}

	// The diff layer must be minimal: changed/added paths and whiteouts only,
	// never the unchanged keep.txt.
	var man Manifest
	if err := l.BlobJSON(manDesc.Digest, &man); err != nil {
		t.Fatal(err)
	}
	if len(man.Layers) != 2 {
		t.Fatalf("layers = %d, want 2", len(man.Layers))
	}
	names := layerEntryNames(t, l, man.Layers[1].Digest)
	// A single whiteout on a removed directory masks its whole subtree, so we
	// expect ".wh.olddir" but not a per-child ".wh.inner".
	want := []string{"added.txt", "etc/hosts", ".wh.delete-me.txt", ".wh.olddir"}
	if !sameSet(names, want) {
		t.Fatalf("diff layer entries = %v, want set %v", names, want)
	}

	// Flattening the result must reproduce the desired end state exactly.
	mem, _, cleanup, err := Flatten(l, "img:1", testDeps())
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	defer cleanup()
	root := mem.RootNode()

	if got := string(readSource(t, child(child(root, "etc"), "hosts").Content)); got != "new\n" {
		t.Errorf("etc/hosts = %q, want new", got)
	}
	if child(root, "keep.txt") == nil {
		t.Error("keep.txt should remain from the base")
	}
	if child(root, "added.txt") == nil {
		t.Error("added.txt should be present")
	}
	if child(root, "delete-me.txt") != nil {
		t.Error("delete-me.txt should have been whiteouted")
	}
	if child(root, "olddir") != nil {
		t.Error("olddir should have been whiteouted")
	}
}

func TestAddLayerDiffEmpty(t *testing.T) {
	// Diffing an image against an identical tree yields a layer with no entries,
	// and the flattened result is unchanged.
	dir := t.TempDir()
	l, _ := CreateLayout(dir)
	build := func() *image.Mem {
		m := image.NewMem(testDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
		m.Root().Create("a.txt", tree.Bytes("a\n"), meta(0o644))
		return m
	}
	if _, err := Build(l, build(), BuildOptions{Ref: "img:1"}); err != nil {
		t.Fatal(err)
	}
	manDesc, err := AddLayerDiff(l, "img:1", build(), BuildOptions{Ref: "img:1"})
	if err != nil {
		t.Fatalf("AddLayerDiff: %v", err)
	}
	var man Manifest
	l.BlobJSON(manDesc.Digest, &man)
	if names := layerEntryNames(t, l, man.Layers[1].Digest); len(names) != 0 {
		t.Errorf("identical diff produced entries %v, want none", names)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}
