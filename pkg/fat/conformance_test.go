//go:build conformance

package fat

import (
	"errors"
	"os"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// TestFsckFATConformance builds a FAT32 image and checks it with the real
// fsck.fat (host or container). Run: go test -tags conformance ./pkg/fat/
func TestFsckFATConformance(t *testing.T) {
	const size = 64 << 20
	f, err := os.CreateTemp(t.TempDir(), "fsforge-*.fat")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	buildSample(t, device.NewFile(f, size))
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}

	out, err := conformance.FsckFAT(f.Name())
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("fsck.fat unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("fsck.fat reported problems: %v\n%s", err, out)
	}
	t.Logf("fsck.fat clean:\n%s", out)
}
