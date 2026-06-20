//go:build conformance

package ext

import (
	"errors"
	"os"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// These tests validate fsforge ext images against the real e2fsprogs (on the
// host or via a container). Run with: go test -tags conformance ./pkg/ext/

func TestExt2Conformance(t *testing.T) { runE2fsck(t, NewExt2(testDeps()), 16<<20, false) }
func TestExt4Conformance(t *testing.T) { runE2fsck(t, NewExt4(testDeps()), 64<<20, false) }

// Mutated images must also pass e2fsck, proving the staged re-layout produces a
// consistent filesystem.
func TestExt2MutationConformance(t *testing.T) { runE2fsck(t, NewExt2(testDeps()), 16<<20, true) }
func TestExt4MutationConformance(t *testing.T) { runE2fsck(t, NewExt4(testDeps()), 64<<20, true) }

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
