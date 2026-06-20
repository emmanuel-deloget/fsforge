//go:build conformance

package partition

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// TestGPTConformance writes a GPT disk and validates it with sfdisk (host or
// container). Run: go test -tags conformance ./pkg/partition/
func TestGPTConformance(t *testing.T) {
	const size = 256 << 20
	f, err := os.CreateTemp(t.TempDir(), "fsforge-*.img")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if _, err := FormatGPT(device.NewFile(f, size), testDeps(), sampleSpecs()); err != nil {
		t.Fatalf("FormatGPT: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}

	out, err := conformance.Sfdisk(f.Name())
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("sfdisk unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("sfdisk failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "gpt") {
		t.Errorf("sfdisk did not report a gpt label:\n%s", out)
	}
	if !strings.Contains(out, "EFI System") {
		t.Errorf("ESP partition not recognised:\n%s", out)
	}
	t.Logf("sfdisk accepted the GPT:\n%s", out)
}

func TestMBRConformance(t *testing.T) {
	const size = 128 << 20
	f, err := os.CreateTemp(t.TempDir(), "fsforge-*.img")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_, err = FormatMBR(device.NewFile(f, size), []MBRSpec{
		{Type: MBRTypeFAT32LBA, Size: 32 << 20, Bootable: true},
		{Type: MBRTypeLinux, Size: 0},
	})
	if err != nil {
		t.Fatalf("FormatMBR: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}

	out, err := conformance.Sfdisk(f.Name())
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("sfdisk unavailable")
	}
	if err != nil {
		t.Fatalf("sfdisk failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "dos") {
		t.Errorf("sfdisk did not report a dos label:\n%s", out)
	}
	t.Logf("sfdisk accepted the MBR:\n%s", out)
}
