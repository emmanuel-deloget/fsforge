//go:build conformance

package ext

import (
	"errors"
	"os"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// These tests validate fsforge ext images against the real e2fsprogs (on the
// host or via a container). Run with: go test -tags conformance ./pkg/ext/

func TestExt2Conformance(t *testing.T) { runE2fsck(t, NewExt2(testDeps()), 16<<20) }
func TestExt4Conformance(t *testing.T) { runE2fsck(t, NewExt4(testDeps()), 64<<20) }

func runE2fsck(t *testing.T, e *Engine, size int64) {
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
