package fsforge_test

import (
	"os"
	"path/filepath"
	"testing"

	fsforge "github.com/emmanuel-deloget/fsforge"
)

func TestAddOCILayer(t *testing.T) {
	// Build an OCI image from a base directory.
	base := t.TempDir()
	mustWrite(t, filepath.Join(base, "app"), "v1")
	ociDir := filepath.Join(t.TempDir(), "oci")
	if err := fsforge.Convert(
		fsforge.Location{Kind: "dir", Path: base},
		fsforge.Location{Kind: "oci", Path: ociDir},
		fsforge.Options{Ref: "app:v1"},
	); err != nil {
		t.Fatalf("Convert dir->oci: %v", err)
	}

	// Append an additive layer from another directory.
	patch := t.TempDir()
	mustWrite(t, filepath.Join(patch, "config.yaml"), "debug: true\n")
	if err := fsforge.AddOCILayer(ociDir, "app:v1",
		fsforge.Location{Kind: "dir", Path: patch},
		fsforge.OCILayerOptions{Gzip: true},
	); err != nil {
		t.Fatalf("AddOCILayer: %v", err)
	}

	// Flatten back to a directory: both files coexist.
	out := t.TempDir()
	if err := fsforge.Convert(
		fsforge.Location{Kind: "oci", Path: ociDir},
		fsforge.Location{Kind: "dir", Path: out},
		fsforge.Options{},
	); err != nil {
		t.Fatalf("Convert oci->dir: %v", err)
	}
	assertFile(t, filepath.Join(out, "app"), "v1")
	assertFile(t, filepath.Join(out, "config.yaml"), "debug: true\n")
}

func TestAddOCILayerDiffRemoves(t *testing.T) {
	base := t.TempDir()
	mustWrite(t, filepath.Join(base, "keep"), "k")
	mustWrite(t, filepath.Join(base, "drop"), "d")
	ociDir := filepath.Join(t.TempDir(), "oci")
	if err := fsforge.Convert(
		fsforge.Location{Kind: "dir", Path: base},
		fsforge.Location{Kind: "oci", Path: ociDir},
		fsforge.Options{Ref: "app:v1"},
	); err != nil {
		t.Fatal(err)
	}

	// The desired end state drops "drop" and keeps "keep".
	next := t.TempDir()
	mustWrite(t, filepath.Join(next, "keep"), "k")
	if err := fsforge.AddOCILayer(ociDir, "app:v1",
		fsforge.Location{Kind: "dir", Path: next},
		fsforge.OCILayerOptions{Diff: true},
	); err != nil {
		t.Fatalf("AddOCILayer diff: %v", err)
	}

	out := t.TempDir()
	if err := fsforge.Convert(
		fsforge.Location{Kind: "oci", Path: ociDir},
		fsforge.Location{Kind: "dir", Path: out},
		fsforge.Options{},
	); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(out, "keep"), "k")
	if _, err := os.Stat(filepath.Join(out, "drop")); !os.IsNotExist(err) {
		t.Errorf("drop should have been whiteouted, stat err = %v", err)
	}
}
