package partition

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"unicode/utf16"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

const (
	sectorSize     = 512
	entryCount     = 128
	entrySize      = 128
	entryArraySecs = entryCount * entrySize / sectorSize // 32
	primaryHdrLBA  = 1
	entryArrayLBA  = 2
	firstUsableLBA = 2 + entryArraySecs // 34
	alignSectors   = 2048               // 1 MiB alignment
)

var le = binary.LittleEndian

// Type is a 16-byte partition type GUID in on-disk (mixed-endian) byte order.
type Type [16]byte

// Common partition type GUIDs, in on-disk byte order.
var (
	TypeEFI       = Type{0x28, 0x73, 0x2A, 0xC1, 0x1F, 0xF8, 0xD2, 0x11, 0xBA, 0x4B, 0x00, 0xA0, 0xC9, 0x3E, 0xC9, 0x3B}
	TypeLinuxData = Type{0xAF, 0x3D, 0xC6, 0x0F, 0x83, 0x84, 0x72, 0x47, 0x8E, 0x79, 0x3D, 0x69, 0xD8, 0x47, 0x7D, 0xE4}
	TypeLinuxRoot = Type{0xE3, 0xBC, 0x68, 0x4F, 0xCD, 0xE8, 0xB1, 0x4D, 0x96, 0xE7, 0xFB, 0xCA, 0xF9, 0x84, 0xB7, 0x09}
)

// Spec describes one partition to create. Size is in bytes and is rounded up to
// the alignment; a Size of 0 means "use all remaining space" and is only valid
// for the last spec.
type Spec struct {
	Type Type
	Name string
	Size int64
}

// Partition is a created partition: its byte range on the disk and the Section
// the caller formats.
type Partition struct {
	Name             string
	StartLBA, EndLBA uint64 // inclusive
	Section          *device.Section
}

var (
	errNoSpecs     = errors.New("partition: no partitions specified")
	errFillNotLast = errors.New("partition: only the last partition may fill remaining space")
	errTooSmall    = errors.New("partition: disk too small for the requested layout")
)

// FormatGPT writes a protective MBR, primary and backup GPT, and returns the
// created partitions (each with a device.Section to format). The disk device
// size must be a multiple of the sector size.
func FormatGPT(dev device.Device, deps image.Deps, specs []Spec) ([]Partition, error) {
	if len(specs) == 0 {
		return nil, errNoSpecs
	}
	if deps.UUID == nil {
		deps.UUID = image.RandomUUID{}
	}
	if dev.Size()%sectorSize != 0 {
		return nil, errors.New("partition: device size must be a multiple of 512")
	}
	totalLBA := uint64(dev.Size()) / sectorSize
	if totalLBA < firstUsableLBA+entryArraySecs+alignSectors {
		return nil, errTooSmall
	}
	lastUsableLBA := totalLBA - 1 - entryArraySecs - 1 // backup header + backup array

	// Assign aligned LBA ranges.
	parts := make([]Partition, len(specs))
	cur := uint64(align(firstUsableLBA, alignSectors))
	for i, s := range specs {
		if s.Size == 0 {
			if i != len(specs)-1 {
				return nil, errFillNotLast
			}
			if cur > lastUsableLBA {
				return nil, errTooSmall
			}
			parts[i] = Partition{Name: s.Name, StartLBA: cur, EndLBA: lastUsableLBA}
			cur = lastUsableLBA + 1
			break
		}
		secs := uint64((s.Size + sectorSize - 1) / sectorSize)
		end := cur + secs - 1
		if end > lastUsableLBA {
			return nil, errTooSmall
		}
		parts[i] = Partition{Name: s.Name, StartLBA: cur, EndLBA: end}
		cur = uint64(align(end+1, alignSectors))
	}

	diskGUID := deps.UUID.UUID()
	entries := make([]byte, entryCount*entrySize)
	for i, s := range specs {
		buildEntry(entries[i*entrySize:], s.Type, deriveGUID(diskGUID, i+1), parts[i].StartLBA, parts[i].EndLBA, s.Name)
		parts[i].Section = device.NewSection(dev,
			int64(parts[i].StartLBA)*sectorSize,
			int64(parts[i].EndLBA-parts[i].StartLBA+1)*sectorSize)
	}
	entriesCRC := crc32.ChecksumIEEE(entries)

	// Protective MBR.
	if err := writeProtectiveMBR(dev, totalLBA); err != nil {
		return nil, err
	}
	// Primary: header at LBA1, array at LBA2.
	if _, err := dev.WriteAt(entries, entryArrayLBA*sectorSize); err != nil {
		return nil, err
	}
	primary := buildHeader(primaryHdrLBA, totalLBA-1, firstUsableLBA, lastUsableLBA, entryArrayLBA, diskGUID, entriesCRC)
	if _, err := dev.WriteAt(primary, primaryHdrLBA*sectorSize); err != nil {
		return nil, err
	}
	// Backup: array just before the last LBA, header at the last LBA.
	backupArrayLBA := totalLBA - 1 - entryArraySecs
	if _, err := dev.WriteAt(entries, int64(backupArrayLBA)*sectorSize); err != nil {
		return nil, err
	}
	backup := buildHeader(totalLBA-1, primaryHdrLBA, firstUsableLBA, lastUsableLBA, backupArrayLBA, diskGUID, entriesCRC)
	if _, err := dev.WriteAt(backup, int64(totalLBA-1)*sectorSize); err != nil {
		return nil, err
	}
	return parts, nil
}

func buildHeader(currentLBA, backupLBA, firstUsable, lastUsable, arrayLBA uint64, diskGUID [16]byte, entriesCRC uint32) []byte {
	b := make([]byte, sectorSize)
	copy(b[0:8], "EFI PART")
	le.PutUint32(b[8:], 0x00010000) // revision 1.0
	le.PutUint32(b[12:], 92)        // header size
	le.PutUint64(b[24:], currentLBA)
	le.PutUint64(b[32:], backupLBA)
	le.PutUint64(b[40:], firstUsable)
	le.PutUint64(b[48:], lastUsable)
	copy(b[56:72], diskGUID[:])
	le.PutUint64(b[72:], arrayLBA)
	le.PutUint32(b[80:], entryCount)
	le.PutUint32(b[84:], entrySize)
	le.PutUint32(b[88:], entriesCRC)
	// header CRC over the first 92 bytes with the CRC field zeroed.
	le.PutUint32(b[16:], 0)
	le.PutUint32(b[16:], crc32.ChecksumIEEE(b[:92]))
	return b
}

func buildEntry(b []byte, typ Type, unique [16]byte, firstLBA, lastLBA uint64, name string) {
	copy(b[0:16], typ[:])
	copy(b[16:32], unique[:])
	le.PutUint64(b[32:], firstLBA)
	le.PutUint64(b[40:], lastLBA)
	// attributes b[48:56] = 0
	enc := utf16.Encode([]rune(name))
	for i := 0; i < len(enc) && i < 36; i++ {
		le.PutUint16(b[56+i*2:], enc[i])
	}
}

func writeProtectiveMBR(dev device.Device, totalLBA uint64) error {
	b := make([]byte, sectorSize)
	// One 0xEE partition covering the disk (minus the MBR), per the GPT spec.
	p := b[446:]
	p[0] = 0x00                         // not bootable
	p[1], p[2], p[3] = 0x00, 0x02, 0x00 // CHS start (legacy, ignored)
	p[4] = 0xEE                         // GPT protective type
	p[5], p[6], p[7] = 0xFF, 0xFF, 0xFF // CHS end
	le.PutUint32(p[8:], 1)              // first LBA
	sz := totalLBA - 1
	if sz > 0xFFFFFFFF {
		sz = 0xFFFFFFFF
	}
	le.PutUint32(p[12:], uint32(sz))
	b[510], b[511] = 0x55, 0xAA
	_, err := dev.WriteAt(b, 0)
	return err
}

// deriveGUID makes a deterministic, distinct GUID from the disk GUID and an
// index, so reproducible disks still have unique partition GUIDs.
func deriveGUID(base [16]byte, index int) [16]byte {
	g := base
	g[15] ^= byte(index)
	g[14] ^= byte(index >> 8)
	return g
}

func align(lba, to uint64) uint64 { return (lba + to - 1) / to * to }
