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

func TestBuilderChaining(t *testing.T) {
	// Host/Deps/BlockSize are configuration setters; exercise the chain.
	b := fsforge.New("ext2").
		Host().
		Deps(fsforge.HostDeps()).
		BlockSize(1024).
		Size("16M").
		Label("chain")
	out := filepath.Join(t.TempDir(), "chain.img")
	if err := b.BuildFromDir(sampleTree(t), out); err != nil {
		t.Fatalf("BuildFromDir: %v", err)
	}
}

func TestBuildFromTree(t *testing.T) {
	// Build an in-memory tree exercising symlinks, hard links and a device
	// node, then lay it out via BuildFromTree (Graft path).
	mem := image.NewMem(fsforge.HostDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	root := mem.Root()
	h, err := root.Create("file", tree.Bytes("payload\n"), tree.Meta{Mode: 0o644})
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Link("hardlink", h); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("sym", "file", tree.Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := root.Mknod("null", 0x0103, tree.Meta{Mode: fs.ModeCharDevice | 0o666}); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "tree.img")
	if err := fsforge.New("ext4").Size("16M").BuildFromTree(mem.RootNode(), out); err != nil {
		t.Fatalf("BuildFromTree: %v", err)
	}

	// Extract back: file + hardlink present, symlink resolves, device skipped.
	back := t.TempDir()
	if err := fsforge.Convert(
		fsforge.Location{Kind: "ext4", Path: out},
		fsforge.Location{Kind: "dir", Path: back},
		fsforge.Options{},
	); err != nil {
		t.Fatalf("Convert ext4->dir: %v", err)
	}
	assertFile(t, filepath.Join(back, "file"), "payload\n")
	assertFile(t, filepath.Join(back, "hardlink"), "payload\n")
	if fi, err := os.Lstat(filepath.Join(back, "sym")); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("symlink not extracted: %v", err)
	}
}

func TestConvertOCIRoundTrip(t *testing.T) {
	src := sampleTree(t)
	ociDir := filepath.Join(t.TempDir(), "oci")
	if err := fsforge.Convert(
		fsforge.Location{Kind: "dir", Path: src},
		fsforge.Location{Kind: "oci", Path: ociDir},
		fsforge.Options{Ref: "app:v1"},
	); err != nil {
		t.Fatalf("Convert dir->oci: %v", err)
	}
	back := t.TempDir()
	if err := fsforge.Convert(
		fsforge.Location{Kind: "oci", Path: ociDir},
		fsforge.Location{Kind: "dir", Path: back},
		fsforge.Options{},
	); err != nil {
		t.Fatalf("Convert oci->dir: %v", err)
	}
	assertFile(t, filepath.Join(back, "hello.txt"), "hello fsforge\n")
}

func TestBuilderISO(t *testing.T) {
	out := filepath.Join(t.TempDir(), "cd.iso")
	if err := fsforge.New("iso").BuildFromDir(sampleTree(t), out); err != nil {
		t.Fatalf("BuildFromDir iso: %v", err)
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Fatalf("iso output missing or empty: %v", err)
	}
}

func TestSourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	if got := fsforge.SourceDateEpoch(); got != 1700000000 {
		t.Fatalf("SourceDateEpoch = %d, want 1700000000", got)
	}
	t.Setenv("SOURCE_DATE_EPOCH", "garbage")
	if got := fsforge.SourceDateEpoch(); got != 0 {
		t.Fatalf("SourceDateEpoch(garbage) = %d, want 0", got)
	}
	os.Unsetenv("SOURCE_DATE_EPOCH")
	if got := fsforge.SourceDateEpoch(); got != 0 {
		t.Fatalf("SourceDateEpoch(unset) = %d, want 0", got)
	}
}

func TestConvertErrors(t *testing.T) {
	// Unknown source and sink kinds must error.
	if err := fsforge.Convert(
		fsforge.Location{Kind: "bogus", Path: "/x"},
		fsforge.Location{Kind: "dir", Path: t.TempDir()},
		fsforge.Options{},
	); err == nil {
		t.Fatal("unknown source kind should fail")
	}
	if err := fsforge.Convert(
		fsforge.Location{Kind: "dir", Path: sampleTree(t)},
		fsforge.Location{Kind: "bogus", Path: "/x"},
		fsforge.Options{},
	); err == nil {
		t.Fatal("unknown sink kind should fail")
	}
}
