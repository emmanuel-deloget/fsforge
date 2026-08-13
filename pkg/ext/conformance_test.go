//go:build conformance

package ext

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// These tests validate fsforge ext images against the real e2fsprogs (on the
// host or via a container). Run with: go test -tags conformance ./pkg/ext/

func TestExt2Conformance(t *testing.T) { runE2fsck(t, NewExt2(testDeps()), 16<<20, false) }
func TestExt4Conformance(t *testing.T) { runE2fsck(t, NewExt4(testDeps()), 64<<20, false) }

// Regression for issue #12: a 4 KiB-block ext4 of 65792 blocks leaves a runt
// final group whose inode table overruns the group; e2fsck (like the kernel)
// rejects that unless the runt group is dropped.
func TestExt4RuntFinalGroupConformance(t *testing.T) {
	runE2fsck(t, NewExt4(testDeps()), 65792*4096, false)
}

// Mutated images must also pass e2fsck, proving the staged re-layout produces a
// consistent filesystem.
func TestExt2MutationConformance(t *testing.T) { runE2fsck(t, NewExt2(testDeps()), 16<<20, true) }
func TestExt4MutationConformance(t *testing.T) { runE2fsck(t, NewExt4(testDeps()), 64<<20, true) }

// A file too large for one extent (32768 blocks) and for the largest run a block
// group can offer must still be laid out, and e2fsprogs must agree: e2fsck finds
// the image clean and debugfs dumps the file back byte for byte. The 1 KiB case
// fragments the file over enough runs that the extent tree needs index nodes of
// its own, which is what validates them against a real implementation.
func TestExt4LargeFileConformance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bs       uint32
		fileSize int64
		devSize  int64
	}{
		{"indexed extent tree", 1024, 40 << 20, 96 << 20},
		{"multi-extent 300MiB", 4096, 300 << 20, 512 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runLargeFileE2fsck(t, tc.bs, tc.fileSize, tc.devSize)
		})
	}
}

func runLargeFileE2fsck(t *testing.T, bs uint32, fileSize, devSize int64) {
	t.Helper()
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "fsforge-*.img")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(devSize); err != nil {
		t.Fatal(err)
	}

	e := NewExt4(testDeps())
	img, err := e.Format(device.NewFile(f, devSize), image.Params{Label: "fsforge", BlockSize: bs})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if _, err := img.Root().Create("big", patternSource{size: fileSize}, meta(0o644)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}

	out, err := conformance.E2fsck(f.Name())
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("e2fsprogs unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("e2fsck reported problems: %v\n%s", err, out)
	}
	t.Logf("e2fsck clean:\n%s", out)

	dumped := filepath.Join(dir, "big.dump")
	out, err = conformance.DebugfsDump(f.Name(), "/big", dumped)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("debugfs unavailable")
	}
	if err != nil {
		t.Fatalf("debugfs dump: %v\n%s", err, out)
	}
	g, err := os.Open(dumped)
	if err != nil {
		t.Fatalf("open dump (debugfs output: %s): %v", out, err)
	}
	defer g.Close()
	if st, err := g.Stat(); err != nil {
		t.Fatal(err)
	} else if st.Size() != fileSize {
		t.Fatalf("dumped size = %d, want %d\n%s", st.Size(), fileSize, out)
	}
	checkPattern(t, io.NewSectionReader(g, 0, fileSize), fileSize)
}

func runE2fsck(t *testing.T, e *Engine, size int64, mutate bool) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "fsforge-*.img")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}

	dev := device.NewFile(f, size)
	buildSampleWith(t, e, dev, 400*1024)

	if mutate {
		opened, err := e.Open(dev)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		root := opened.Root()
		if _, err := root.Create("added.txt", tree.Bytes("mutated\n"), meta(0o644)); err != nil {
			t.Fatal(err)
		}
		if err := root.Remove("shortlink"); err != nil {
			t.Fatal(err)
		}
		if err := opened.Finalize(); err != nil {
			t.Fatalf("Finalize (mutate): %v", err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}

	out, err := conformance.E2fsck(f.Name())
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("e2fsprogs unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("e2fsck reported problems: %v\n%s", err, out)
	}
	t.Logf("e2fsck clean:\n%s", out)
}

// TestXattrConformance puts both attribute homes in front of e2fsck: a set
// small enough to live in the space left inside the inode, and one large enough
// to need a block of its own. The block is the interesting case — it carries
// per-entry and per-block hashes that e2fsck recomputes, so a wrong hash shows
// up here and nowhere else.
func TestXattrConformance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		xattrs map[string][]byte
	}{{
		name:   "inline",
		xattrs: map[string][]byte{"security.capability": []byte("0123456789abcdefghij")},
	}, {
		name: "block",
		xattrs: map[string][]byte{
			"security.selinux":    []byte("system_u:object_r:etc_t:s0\x00"),
			"security.capability": []byte("0123456789abcdefghij"),
			"user.comment":        []byte(strings.Repeat("x", 120)),
			"trusted.overlay.foo": []byte("bar"),
		},
	}, {
		name: "empty value",
		xattrs: map[string][]byte{
			"user.empty": nil,
			"user.one":   []byte("1"),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "fsforge-*.img")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			const size = 64 << 20
			if err := f.Truncate(size); err != nil {
				t.Fatal(err)
			}

			img, err := NewExt4(testDeps()).Format(device.NewFile(f, size),
				image.Params{Label: "fsforge"})
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			m := meta(0o644)
			m.Xattrs = tc.xattrs
			if _, err := img.Root().Create("file", tree.Bytes("payload\n"), m); err != nil {
				t.Fatalf("Create: %v", err)
			}
			dm := meta(fs.ModeDir | 0o755)
			dm.Xattrs = tc.xattrs
			if _, err := img.Root().Mkdir("dir", dm); err != nil {
				t.Fatalf("Mkdir: %v", err)
			}
			if err := img.Finalize(); err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			if err := f.Sync(); err != nil {
				t.Fatal(err)
			}

			out, err := conformance.E2fsck(f.Name())
			if errors.Is(err, conformance.ErrUnavailable) {
				t.Skip("e2fsprogs unavailable (no host binary or container runtime)")
			}
			if err != nil {
				t.Fatalf("e2fsck reported problems: %v\n%s", err, out)
			}
		})
	}
}
