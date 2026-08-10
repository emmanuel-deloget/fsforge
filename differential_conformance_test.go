//go:build conformance

package fsforge_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/internal/fsgen"
	"github.com/emmanuel-deloget/fsforge/internal/manifest"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// The tests here answer the question a round trip through fsforge alone cannot:
// does the image mean the same thing to somebody else? A writer and a reader
// that share a misreading of the format agree with each other perfectly.
//
// Both are run unprivileged, which bounds what can be checked. Extraction
// cannot recreate device nodes or restore owners without CAP_MKNOD and root, so
// those are left out of the generated trees and out of the compared fields —
// not because the format loses them, but because the harness cannot observe
// them. The in-process round trip in differential_test.go covers those.
//
// Run: go test -tags conformance -run Differential .

// hostCaps is what an unprivileged extraction can carry back.
var hostCaps = fsgen.Caps{Symlinks: true, HardLinks: true, NonUTF8: true, Times: true}

// hostFields is what can be compared through a host directory: no owner (the
// extraction lands as the running user) and no link count for formats that
// duplicate rather than share.
const hostFields = manifest.Type | manifest.Perm | manifest.MTime |
	manifest.Size | manifest.Content | manifest.Link

type extractCase struct {
	fs       string
	extract  func(image, dest string) (string, error)
	fields   manifest.Field
	minted   []string
	caps     fsgen.Caps
	skipDirT bool // the tool restamps directories on extraction
}

// TestDifferentialExtract writes an image with fsforge, extracts it with the
// format's own tool, and compares a full manifest of the result against the
// tree that went in.
func TestDifferentialExtract(t *testing.T) {
	cases := []extractCase{{
		fs:      "squashfs",
		extract: conformance.Unsquashfs,
		fields:  hostFields,
		caps:    hostCaps,
	}, {
		fs:      "erofs",
		extract: conformance.ErofsExtract,
		fields:  hostFields,
		caps:    hostCaps,
	}, {
		// Timestamps are left out here, not because the archive lacks them — the
		// in-process round trip checks mtime on cpio and passes, so fsforge writes
		// them correctly — but because extraction does not put them back
		// faithfully: cpio stamps each directory as it writes into it, and even
		// with -m some entries come back with the time of extraction.
		fs:      "cpio",
		extract: conformance.CpioExtract,
		fields:  hostFields &^ manifest.MTime,
		caps:    hostCaps,
	}, {
		// Same reasoning for xorriso, which restores Rock Ridge names and modes
		// but not every timestamp. Depth and name length are bounded as in the
		// in-process test.
		fs:      "iso",
		extract: conformance.XorrisoExtract,
		fields:  hostFields &^ manifest.MTime,
		caps: fsgen.Caps{MaxName: 64, MaxLink: 48, MaxDepth: 5,
			Symlinks: true, Times: true},
	}}

	for _, tc := range cases {
		t.Run(tc.fs, func(t *testing.T) {
			tmp := t.TempDir()
			imgPath := filepath.Join(tmp, "image."+tc.fs)

			src, err := fsgen.Generate(fsforge.ReproducibleDeps(1600000000),
				fsgen.Options{Seed: *seed, Caps: tc.caps})
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			want, err := manifest.FromTree(src.RootNode())
			if err != nil {
				t.Fatal(err)
			}
			writeImage(t, tc.fs, src.RootNode(), imgPath)

			dest := filepath.Join(tmp, "extracted")
			out, err := tc.extract(imgPath, dest)
			if errors.Is(err, conformance.ErrUnavailable) {
				t.Skipf("%s extraction tool unavailable", tc.fs)
			}
			if err != nil {
				t.Fatalf("extract: %v\n%s", err, out)
			}

			got, err := manifest.FromDir(dest)
			if err != nil {
				t.Fatalf("manifest of extraction: %v", err)
			}
			diffs := manifest.Diff(want, got, manifest.Options{
				Fields: tc.fields, Minted: tc.minted, SkipDirMTime: tc.skipDirT,
			})
			if len(diffs) > 0 {
				t.Errorf("%s: fsforge and %s disagree over %d entries (seed %d)\n%s",
					tc.fs, tc.fs, len(want), *seed, strings.Join(diffs, "\n"))
			}
		})
	}
}

// TestDifferentialIngest is the other direction: the format's own tool builds
// the image from a host directory, and fsforge reads it back.
func TestDifferentialIngest(t *testing.T) {
	cases := []struct {
		fs     string
		make   func(srcDir, image string) (string, error)
		fields manifest.Field
		caps   fsgen.Caps
	}{{
		fs:     "erofs",
		make:   conformance.MakeErofs,
		fields: hostFields,
		caps:   fsgen.Caps{Symlinks: true, NonUTF8: true, Times: true},
	}, {
		fs:     "cpio",
		make:   conformance.MakeCpio,
		fields: hostFields,
		caps:   fsgen.Caps{Symlinks: true, NonUTF8: true, Times: true},
	}, {
		fs:     "romfs",
		make:   conformance.MakeRomfs,
		fields: manifest.Type | manifest.Size | manifest.Content | manifest.Link,
		caps:   fsgen.Caps{Symlinks: true, NonUTF8: true},
	}}

	for _, tc := range cases {
		t.Run(tc.fs, func(t *testing.T) {
			tmp := t.TempDir()
			srcDir := filepath.Join(tmp, "src")
			imgPath := filepath.Join(tmp, "image."+tc.fs)

			gen, err := fsgen.Generate(fsforge.ReproducibleDeps(1600000000),
				fsgen.Options{Seed: *seed, Caps: tc.caps})
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			// The manifest of what actually landed on disk, not of what was asked
			// for: the host may have clamped a mode or dropped a node.
			want, err := fsgen.WriteDir(gen.RootNode(), srcDir)
			if err != nil {
				t.Fatalf("write host tree: %v", err)
			}

			out, err := tc.make(srcDir, imgPath)
			if errors.Is(err, conformance.ErrUnavailable) {
				t.Skipf("%s creation tool unavailable", tc.fs)
			}
			if err != nil {
				t.Fatalf("build with the reference tool: %v\n%s", err, out)
			}

			got := readImage(t, tc.fs, imgPath)
			diffs := manifest.Diff(want, got, manifest.Options{Fields: tc.fields})
			if len(diffs) > 0 {
				t.Errorf("fsforge misreads a %s image built by its own tool, over %d entries (seed %d)\n%s",
					tc.fs, len(want), *seed, strings.Join(diffs, "\n"))
			}
		})
	}
}

// writeImage lays root out with the named engine and writes the bytes to path.
func writeImage(t *testing.T, fstype string, root *image.Node, path string) {
	t.Helper()
	deps := fsforge.ReproducibleDeps(1600000000)
	eng, err := fsforge.EngineFor(fstype, deps, 0)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewMem(64 << 20)
	img, err := eng.Format(dev, image.Params{Label: "diff"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if err := fsforge.Graft(img.Root(), root); err != nil {
		t.Fatalf("Graft: %v", err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := os.WriteFile(path, dev.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readImage opens an image file with the named engine and manifests its tree.
func readImage(t *testing.T, fstype, path string) manifest.Manifest {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := fsforge.EngineFor(fstype, fsforge.ReproducibleDeps(1600000000), 0)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := eng.Open(device.NewFile(f, fi.Size()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m, err := manifest.FromTree(opened.(interface{ RootNode() *image.Node }).RootNode())
	if err != nil {
		t.Fatal(err)
	}
	return m
}
