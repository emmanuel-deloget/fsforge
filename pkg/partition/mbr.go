package partition

import (
	"errors"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// MBR partition type bytes.
const (
	MBRTypeEmpty     = 0x00
	MBRTypeFAT32LBA  = 0x0C
	MBRTypeLinux     = 0x83
	MBRTypeLinuxSwap = 0x82
	MBRTypeEFI       = 0xEF
)

// MBRSpec describes one MBR primary partition. Size is in bytes, rounded up to
// the alignment; a Size of 0 fills the remaining space (last spec only).
type MBRSpec struct {
	Type     byte
	Size     int64
	Bootable bool
}

var errTooManyPrimaries = errors.New("partition: MBR supports at most 4 primary partitions")

// FormatMBR writes a classic MBR partition table (up to four primaries) and
// returns the created partitions, each with a device.Section to format.
func FormatMBR(dev device.Device, specs []MBRSpec) ([]Partition, error) {
	if len(specs) == 0 {
		return nil, errNoSpecs
	}
	if len(specs) > 4 {
		return nil, errTooManyPrimaries
	}
	if dev.Size()%sectorSize != 0 {
		return nil, errors.New("partition: device size must be a multiple of 512")
	}
	totalLBA := uint64(dev.Size()) / sectorSize
	if totalLBA < alignSectors+alignSectors {
		return nil, errTooSmall
	}

	b := make([]byte, sectorSize)
	parts := make([]Partition, len(specs))
	cur := uint64(alignSectors) // first partition aligned to 1 MiB
	for i, s := range specs {
		var start, count uint64
		if s.Size == 0 {
			if i != len(specs)-1 {
				return nil, errFillNotLast
			}
			start, count = cur, totalLBA-cur
		} else {
			start = cur
			count = uint64((s.Size + sectorSize - 1) / sectorSize)
			if start+count > totalLBA {
				return nil, errTooSmall
			}
		}
		writeMBREntry(b[446+i*16:], s, start, count)
		parts[i] = Partition{
			Name:     mbrName(s.Type),
			StartLBA: start,
			EndLBA:   start + count - 1,
			Section:  device.NewSection(dev, int64(start)*sectorSize, int64(count)*sectorSize),
		}
		cur = align(start+count, alignSectors)
	}
	b[510], b[511] = 0x55, 0xAA
	_, err := dev.WriteAt(b, 0)
	return parts, err
}

func writeMBREntry(e []byte, s MBRSpec, startLBA, count uint64) {
	if s.Bootable {
		e[0] = 0x80
	}
	// CHS fields set to the "use LBA" sentinel; modern tools read the LBA fields.
	e[1], e[2], e[3] = 0xFE, 0xFF, 0xFF
	e[4] = s.Type
	e[5], e[6], e[7] = 0xFE, 0xFF, 0xFF
	le.PutUint32(e[8:], uint32(startLBA))
	le.PutUint32(e[12:], uint32(count))
}

func mbrName(t byte) string {
	switch t {
	case MBRTypeEFI:
		return "esp"
	case MBRTypeFAT32LBA:
		return "fat32"
	case MBRTypeLinuxSwap:
		return "swap"
	default:
		return "linux"
	}
}
