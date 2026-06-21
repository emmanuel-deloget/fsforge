package fat

import "errors"

const (
	sectorSize    = 512
	numFATs       = 2
	rootCluster   = 2
	fsInfoSector  = 1
	backupBootSec = 6
	mediaByte     = 0xF8

	// FAT type cluster-count thresholds (FAT spec).
	maxFAT12Clusters = 4084
	maxFAT16Clusters = 65524

	rootEntries1216 = 512 // fixed root directory entries for FAT12/16

	attrReadOnly  = 0x01
	attrHidden    = 0x02
	attrSystem    = 0x04
	attrVolumeID  = 0x08
	attrDirectory = 0x10
	attrArchive   = 0x20
	attrLongName  = 0x0F

	dirEntrySize = 32
)

var (
	errTooSmall = errors.New("fat: device too small for a valid FAT volume")
	errBadSize  = errors.New("fat: device size must be a positive multiple of 512")
)

// geometry derives the FAT layout from a device size in bytes. It supports
// FAT12/16 (fixed root directory) and FAT32 (root in the cluster heap).
type geometry struct {
	fatBits        int // 12, 16 or 32
	totalSectors   uint64
	reservedSecs   uint64
	secPerClus     uint32
	fatSizeSectors uint64
	rootEntCount   uint32 // 0 for FAT32
	rootDirSectors uint64 // 0 for FAT32
	clusters       uint64
	dataStartSec   uint64 // first sector of the data region (cluster 2)
}

func computeGeometry(devSize int64, forceBits int) (geometry, error) {
	var g geometry
	if devSize <= 0 || devSize%sectorSize != 0 {
		return g, errBadSize
	}
	g.totalSectors = uint64(devSize) / sectorSize

	// Choose FAT type and cluster size by volume size (mkfs.fat-style), unless
	// forced.
	switch forceBits {
	case 12:
		g.fatBits, g.secPerClus = 12, 1
	case 16:
		g.fatBits, g.secPerClus = 16, pickSecPerClus16(g.totalSectors)
	case 32:
		g.fatBits, g.secPerClus = 32, fat32SecPerClus(g.totalSectors)
	default:
		switch {
		case g.totalSectors < 8400: // < ~4 MiB
			g.fatBits, g.secPerClus = 12, 1
		case g.totalSectors < 1048576: // < 512 MiB
			g.fatBits, g.secPerClus = 16, pickSecPerClus16(g.totalSectors)
		default:
			g.fatBits, g.secPerClus = 32, fat32SecPerClus(g.totalSectors)
		}
	}

	if g.fatBits == 32 {
		g.reservedSecs = 32
	} else {
		g.reservedSecs = 1
		g.rootEntCount = rootEntries1216
		g.rootDirSectors = (uint64(g.rootEntCount)*dirEntrySize + sectorSize - 1) / sectorSize
	}
	if g.totalSectors < g.reservedSecs+g.rootDirSectors+numFATs+8 {
		return g, errTooSmall
	}

	// Solve the FAT size: grow until the FAT can address every data cluster plus
	// the two reserved entries (fsck derives the cluster count from the BPB).
	bits := uint64(g.fatBits)
	g.fatSizeSectors = 1
	for {
		usable := g.totalSectors - g.reservedSecs - g.rootDirSectors - numFATs*g.fatSizeSectors
		g.clusters = usable / uint64(g.secPerClus)
		needBytes := ((g.clusters+2)*bits + 7) / 8
		needSecs := (needBytes + sectorSize - 1) / sectorSize
		if needSecs <= g.fatSizeSectors {
			break
		}
		g.fatSizeSectors = needSecs
	}
	g.dataStartSec = g.reservedSecs + numFATs*g.fatSizeSectors + g.rootDirSectors

	if err := g.validate(); err != nil {
		return g, err
	}
	return g, nil
}

func (g geometry) validate() error {
	switch g.fatBits {
	case 12:
		if g.clusters < 1 || g.clusters > maxFAT12Clusters {
			return errTooSmall
		}
	case 16:
		if g.clusters <= maxFAT12Clusters || g.clusters > maxFAT16Clusters {
			return errTooSmall
		}
	case 32:
		if g.clusters <= maxFAT16Clusters {
			return errTooSmall
		}
	}
	return nil
}

func pickSecPerClus16(totalSectors uint64) uint32 {
	switch {
	case totalSectors <= 32680:
		return 2
	case totalSectors <= 262144:
		return 4
	case totalSectors <= 524288:
		return 8
	default:
		return 16
	}
}

func fat32SecPerClus(totalSectors uint64) uint32 {
	switch {
	case totalSectors <= 532480: // <= 260 MiB
		return 1
	case totalSectors <= 16777216: // <= 8 GiB
		return 8
	default:
		return 32
	}
}

func (g geometry) clusterSizeBytes() uint32 { return g.secPerClus * sectorSize }

func (g geometry) clusterSector(cluster uint32) uint64 {
	return g.dataStartSec + uint64(cluster-2)*uint64(g.secPerClus)
}

// rootRegionSector is the first sector of the fixed root directory (FAT12/16).
func (g geometry) rootRegionSector() uint64 {
	return g.reservedSecs + numFATs*g.fatSizeSectors
}

func (g geometry) eoc() uint32 {
	switch g.fatBits {
	case 12:
		return 0x0FFF
	case 16:
		return 0xFFFF
	default:
		return 0x0FFFFFFF
	}
}
