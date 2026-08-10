package oci

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// flattenLayer builds a one-layer layout around entries and flattens it.
func flattenLayer(t *testing.T, entries []tarEntry) (*image.Mem, error) {
	t.Helper()
	l, err := CreateLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := l.PutBlobBytes(MediaTypeLayerTar, makeLayer(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	cfgJSON, _ := json.Marshal(Image{Architecture: "amd64", OS: "linux"})
	cfgDesc, err := l.PutBlobBytes(MediaTypeConfig, cfgJSON)
	if err != nil {
		t.Fatal(err)
	}
	manJSON, _ := json.Marshal(Manifest{
		SchemaVersion: 2, MediaType: MediaTypeManifest,
		Config: cfgDesc, Layers: []Descriptor{d},
	})
	manDesc, err := l.PutBlobBytes(MediaTypeManifest, manJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.WriteIndex(manDesc, "img:latest"); err != nil {
		t.Fatal(err)
	}
	mem, _, cleanup, err := Flatten(l, "", testDeps())
	t.Cleanup(cleanup)
	return mem, err
}

// TestFlattenRejectsEscapingNames covers the head of the chain: path.Clean does
// not remove a leading "..", so without the check ensureDir would create
// directories literally named "..", which ExtractToDir then walks out of the
// destination on.
func TestFlattenRejectsEscapingNames(t *testing.T) {
	for _, name := range []string{
		"../../etc/passwd",
		"../escape",
		"etc/../../escape",
		"good/../../../escape",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := flattenLayer(t, []tarEntry{
				{name: name, typeflag: tar.TypeReg, mode: 0o644, body: "owned"},
			})
			if !errors.Is(err, image.ErrBadName) {
				t.Fatalf("Flatten(%q) = %v, want ErrBadName", name, err)
			}
		})
	}
}

// TestFlattenRejectsEscapingWhiteout does the same for a whiteout marker, which
// takes a different branch: it deletes rather than creates, but still resolves a
// parent directory from the untrusted name first.
func TestFlattenRejectsEscapingWhiteout(t *testing.T) {
	_, err := flattenLayer(t, []tarEntry{
		{name: "../.wh.passwd", typeflag: tar.TypeReg},
	})
	if !errors.Is(err, image.ErrBadName) {
		t.Fatalf("Flatten(escaping whiteout) = %v, want ErrBadName", err)
	}
}

// TestFlattenAcceptsOrdinaryNames guards the other direction: absolute names and
// "./" prefixes are ordinary in real layers and must still flatten, as must a
// file whose name merely starts with dots.
func TestFlattenAcceptsOrdinaryNames(t *testing.T) {
	mem, err := flattenLayer(t, []tarEntry{
		{name: "/etc/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "/etc/hosts", typeflag: tar.TypeReg, mode: 0o644, body: "h"},
		{name: "./var/..keep", typeflag: tar.TypeReg, mode: 0o644, body: "k"},
	})
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	root := mem.RootNode()
	if etc := child(root, "etc"); etc == nil || child(etc, "hosts") == nil {
		t.Error("absolute layer name did not land at /etc/hosts")
	}
	if v := child(root, "var"); v == nil || child(v, "..keep") == nil {
		t.Error("name starting with dots was rejected")
	}
}

// TestCheckPathMessage documents that the offending name reaches the caller —
// a build failing on a hostile layer should say which entry caused it.
func TestCheckPathMessage(t *testing.T) {
	err := checkPath("a/../../b")
	if err == nil || !strings.Contains(err.Error(), `".."`) {
		t.Fatalf("checkPath error = %v, want it to name the bad component", err)
	}
}
