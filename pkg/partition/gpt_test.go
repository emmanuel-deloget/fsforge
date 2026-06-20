package partition

import (
	"bytes"
	"hash/crc32"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

func testDeps() image.Deps {
	return image.Deps{UUID: image.FixedUUID{V: [16]byte{0x11, 0x22, 0x33, 0x44, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}}
}

func sampleSpecs() []Spec {
	return []Spec{
		{Type: TypeEFI, Name: "EFI System", Size: 48 << 20},
		{Type: TypeLinuxRoot, Name: "root", Size: 0},
	}
}

func TestGPTStructure(t *testing.T) {
	dev := device.NewMem(256 << 20)
	parts, err := FormatGPT(dev, testDeps(), sampleSpecs())
	if err != nil {
		t.Fatalf("FormatGPT: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d partitions", len(parts))
	}
	b := dev.Bytes()

	// Protective MBR.
	if b[510] != 0x55 || b[511] != 0xAA || b[446+4] != 0xEE {
		t.Errorf("bad protective MBR")
	}
	// Primary header signature + revision.
	hdr := b[sectorSize : 2*sectorSize]
	if string(hdr[0:8]) != "EFI PART" {
		t.Fatalf("bad GPT signature %q", hdr[0:8])
	}
	// Header CRC must validate (field zeroed during compute).
	stored := le.Uint32(hdr[16:])
	tmp := append([]byte(nil), hdr[:92]...)
	le.PutUint32(tmp[16:], 0)
	if got := crc32.ChecksumIEEE(tmp); got != stored {
		t.Errorf("header CRC mismatch: got %#x stored %#x", got, stored)
	}
	// Entry array CRC.
	entries := b[entryArrayLBA*sectorSize : entryArrayLBA*sectorSize+entryCount*entrySize]
	if le.Uint32(hdr[88:]) != crc32.ChecksumIEEE(entries) {
		t.Errorf("entry array CRC mismatch")
	}

	// First partition aligned to 1 MiB.
	if parts[0].StartLBA != alignSectors {
		t.Errorf("part0 start = %d, want %d", parts[0].StartLBA, alignSectors)
	}
	// Backup header at last LBA.
	last := b[len(b)-sectorSize:]
	if string(last[0:8]) != "EFI PART" {
		t.Errorf("missing backup GPT header")
	}
}

func TestGPTReproducible(t *testing.T) {
	d1 := device.NewMem(256 << 20)
	d2 := device.NewMem(256 << 20)
	if _, err := FormatGPT(d1, testDeps(), sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	if _, err := FormatGPT(d2, testDeps(), sampleSpecs()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
		t.Fatal("identical inputs produced different GPT disks")
	}
}

func TestGPTSectionsCoverPartitions(t *testing.T) {
	dev := device.NewMem(256 << 20)
	parts, err := FormatGPT(dev, testDeps(), sampleSpecs())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		wantSize := int64(p.EndLBA-p.StartLBA+1) * sectorSize
		if p.Section.Size() != wantSize {
			t.Errorf("%s section size = %d, want %d", p.Name, p.Section.Size(), wantSize)
		}
	}
	// Partitions must not overlap.
	if parts[1].StartLBA <= parts[0].EndLBA {
		t.Errorf("partitions overlap: %d <= %d", parts[1].StartLBA, parts[0].EndLBA)
	}
}

func TestFillNotLast(t *testing.T) {
	dev := device.NewMem(256 << 20)
	_, err := FormatGPT(dev, testDeps(), []Spec{
		{Type: TypeLinuxData, Name: "a", Size: 0},
		{Type: TypeLinuxData, Name: "b", Size: 1 << 20},
	})
	if err == nil {
		t.Fatal("expected error: only the last partition may fill")
	}
}
