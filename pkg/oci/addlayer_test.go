package oci

import (
	"io/fs"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func TestAddLayerAdditive(t *testing.T) {
	dir := t.TempDir()
	l, err := CreateLayout(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Base image: etc/hosts="old" and a file only the base provides.
	base := image.NewMem(testDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	etc, _ := base.Root().Mkdir("etc", meta(fs.ModeDir|0o755))
	etc.Create("hosts", tree.Bytes("old\n"), meta(0o644))
	base.Root().Create("base-only.txt", tree.Bytes("base\n"), meta(0o644))
	if _, err := Build(l, base, BuildOptions{Ref: "img:1", Gzip: true}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Top layer: overwrite etc/hosts and add a new file.
	top := image.NewMem(testDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	tetc, _ := top.Root().Mkdir("etc", meta(fs.ModeDir|0o755))
	tetc.Create("hosts", tree.Bytes("new\n"), meta(0o644))
	top.Root().Create("top-only.txt", tree.Bytes("top\n"), meta(0o644))

	manDesc, err := AddLayer(l, "img:1", top, BuildOptions{Ref: "img:1", Gzip: true})
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}

	// The manifest must now carry two layers and the config two diff_ids.
	var man Manifest
	if err := l.BlobJSON(manDesc.Digest, &man); err != nil {
		t.Fatal(err)
	}
	if len(man.Layers) != 2 {
		t.Fatalf("layers = %d, want 2", len(man.Layers))
	}
	var cfg Image
	if err := l.BlobJSON(man.Config.Digest, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.RootFS.DiffIDs) != 2 || len(cfg.History) != 2 {
		t.Fatalf("diff_ids=%d history=%d, want 2 and 2", len(cfg.RootFS.DiffIDs), len(cfg.History))
	}

	// Flatten and verify: union of both layers, with the top layer winning.
	mem, _, cleanup, err := Flatten(l, "img:1", testDeps())
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	defer cleanup()
	root := mem.RootNode()

	if got := string(readSource(t, child(root, "base-only.txt").Content)); got != "base\n" {
		t.Errorf("base-only.txt = %q, want base", got)
	}
	if got := string(readSource(t, child(root, "top-only.txt").Content)); got != "top\n" {
		t.Errorf("top-only.txt = %q, want top", got)
	}
	hosts := child(child(root, "etc"), "hosts")
	if got := string(readSource(t, hosts.Content)); got != "new\n" {
		t.Errorf("etc/hosts = %q, want new (top layer overwrites)", got)
	}
}

func TestAddLayerDefaultsRefToBase(t *testing.T) {
	dir := t.TempDir()
	l, _ := CreateLayout(dir)
	base := image.NewMem(testDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	base.Root().Create("a", tree.Bytes("a"), meta(0o644))
	if _, err := Build(l, base, BuildOptions{Ref: "app:v1"}); err != nil {
		t.Fatal(err)
	}
	top := image.NewMem(testDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	top.Root().Create("b", tree.Bytes("b"), meta(0o644))
	// Empty opt.Ref must inherit baseRef, so the tag still resolves afterwards.
	if _, err := AddLayer(l, "app:v1", top, BuildOptions{}); err != nil {
		t.Fatalf("AddLayer: %v", err)
	}
	if _, _, _, err := Flatten(l, "app:v1", testDeps()); err != nil {
		t.Fatalf("tag app:v1 lost after AddLayer: %v", err)
	}
}
