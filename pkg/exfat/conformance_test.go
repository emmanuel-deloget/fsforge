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

// TestReadMkfsExFAT formats a volume with the real mkfs.exfat and reads it back
// with our parser. mkfs.exfat produces an empty filesystem (populating it would
// need a privileged mount), so this validates that we parse a tool-written boot
// sector, FAT and root directory without choking and yield an empty tree.
// Run: go test -tags conformance ./pkg/exfat/
func TestReadMkfsExFAT(t *testing.T) {
	const size = 32 << 20
	f, err := os.CreateTemp(t.TempDir(), "fsforge-mkfs-*.exfat")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	path := f.Name()

	combined, err := conformance.MakeExFAT(path)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("mkfs.exfat unavailable")
	}
	if err != nil {
		t.Fatalf("mkfs.exfat failed: %v\n%s", err, combined)
	}

	r, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	info, _ := r.Stat()

	opened, err := New(testDeps()).Open(device.NewFile(r, info.Size()))
	if err != nil {
		t.Fatalf("Open real exFAT volume: %v", err)
	}
	if n := len(opened.(rootNoder).RootNode().Children); n != 0 {
		t.Errorf("freshly formatted exFAT should be empty, got %d children", n)
	}
}
