//go:build conformance

package exfat

import (
	"errors"
	"os"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// TestFsckExFATConformance builds an exFAT image and checks it with the real
// fsck.exfat (host or container). Run: go test -tags conformance ./pkg/exfat/
func TestFsckExFATConformance(t *testing.T) {
	const size = 64 << 20
	f, err := os.CreateTemp(t.TempDir(), "fsforge-*.exfat")
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

	out, err := conformance.FsckExFAT(f.Name())
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("fsck.exfat unavailable")
	}
	if err != nil || !conformance.CheckExFATClean(out) {
		t.Fatalf("fsck.exfat reported problems (err=%v):\n%s", err, out)
	}
	t.Logf("fsck.exfat clean:\n%s", out)
}
