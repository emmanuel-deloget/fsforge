package partition

import (
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

func TestFormatGPTErrors(t *testing.T) {
	t.Run("no specs", func(t *testing.T) {
		if _, err := FormatGPT(device.NewMem(64<<20), testDeps(), nil); err == nil {
			t.Fatal("empty specs should fail")
		}
	})
	t.Run("odd size", func(t *testing.T) {
		if _, err := FormatGPT(device.NewMem(64<<20+1), testDeps(), sampleSpecs()); err == nil {
			t.Fatal("non-512-multiple device should fail")
		}
	})
	t.Run("disk too small", func(t *testing.T) {
		if _, err := FormatGPT(device.NewMem(64<<10), testDeps(), sampleSpecs()); err == nil {
			t.Fatal("tiny disk should fail")
		}
	})
	t.Run("partition larger than disk", func(t *testing.T) {
		_, err := FormatGPT(device.NewMem(16<<20), testDeps(), []Spec{
			{Type: TypeLinuxData, Name: "huge", Size: 64 << 20},
		})
		if err == nil {
			t.Fatal("oversized partition should fail")
		}
	})
	t.Run("nil UUID source defaults", func(t *testing.T) {
		// A zero Deps must be tolerated (random GUIDs); just assert it succeeds.
		if _, err := FormatGPT(device.NewMem(256<<20), image.Deps{}, sampleSpecs()); err != nil {
			t.Fatalf("FormatGPT with default deps: %v", err)
		}
	})
}

func TestFormatMBRTypesAndFill(t *testing.T) {
	dev := device.NewMem(256 << 20)
	parts, err := FormatMBR(dev, []MBRSpec{
		{Type: MBRTypeEFI, Size: 32 << 20, Bootable: true},
		{Type: MBRTypeLinuxSwap, Size: 16 << 20},
		{Type: MBRTypeLinux, Size: 0}, // fill the rest
	})
	if err != nil {
		t.Fatalf("FormatMBR: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("got %d partitions, want 3", len(parts))
	}
	// Bootable flag on the first entry.
	b := dev.Bytes()
	if b[446] != 0x80 {
		t.Errorf("first partition not marked bootable: %#x", b[446])
	}
	if b[510] != 0x55 || b[511] != 0xAA {
		t.Errorf("missing MBR boot signature")
	}

	// mbrName covers every arm.
	for typ, want := range map[byte]string{
		MBRTypeEFI:      "esp",
		MBRTypeFAT32LBA: "fat32",
		MBRTypeLinuxSwap: "swap",
		MBRTypeLinux:    "linux",
	} {
		if got := mbrName(typ); got != want {
			t.Errorf("mbrName(%#x) = %q, want %q", typ, got, want)
		}
	}
}

func TestFormatMBRFillNotLast(t *testing.T) {
	_, err := FormatMBR(device.NewMem(256<<20), []MBRSpec{
		{Type: MBRTypeLinux, Size: 0},
		{Type: MBRTypeLinux, Size: 1 << 20},
	})
	if err == nil {
		t.Fatal("only the last MBR partition may fill")
	}
}
