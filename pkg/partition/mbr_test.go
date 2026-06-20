package partition

import (
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

func TestMBRStructure(t *testing.T) {
	dev := device.NewMem(128 << 20)
	parts, err := FormatMBR(dev, []MBRSpec{
		{Type: MBRTypeFAT32LBA, Size: 32 << 20, Bootable: true},
		{Type: MBRTypeLinux, Size: 0},
	})
	if err != nil {
		t.Fatalf("FormatMBR: %v", err)
	}
	b := dev.Bytes()
	if b[510] != 0x55 || b[511] != 0xAA {
		t.Fatalf("missing MBR signature")
	}
	if b[446] != 0x80 {
		t.Errorf("first partition not marked bootable")
	}
	if b[446+4] != MBRTypeFAT32LBA {
		t.Errorf("partition 1 type = %#x", b[446+4])
	}
	if b[462+4] != MBRTypeLinux {
		t.Errorf("partition 2 type = %#x", b[462+4])
	}
	if parts[0].StartLBA != alignSectors {
		t.Errorf("partition 1 start = %d, want %d", parts[0].StartLBA, alignSectors)
	}
	if parts[1].StartLBA <= parts[0].EndLBA {
		t.Errorf("partitions overlap")
	}
	// LBA start of first partition in entry must match.
	if le.Uint32(b[446+8:]) != uint32(parts[0].StartLBA) {
		t.Errorf("entry LBA mismatch")
	}
}

func TestMBRTooManyPrimaries(t *testing.T) {
	dev := device.NewMem(128 << 20)
	specs := make([]MBRSpec, 5)
	for i := range specs {
		specs[i] = MBRSpec{Type: MBRTypeLinux, Size: 1 << 20}
	}
	if _, err := FormatMBR(dev, specs); err == nil {
		t.Fatal("expected error for >4 primaries")
	}
}
