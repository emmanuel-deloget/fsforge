package fat

import "errors"

const (
	sectorSize       = 512
	reservedSecs     = 32
	numFATs          = 2
	rootCluster      = 2
	fsInfoSector     = 1
	backupBootSec    = 6
	mediaByte        = 0xF8
	entriesPerFATsec = sectorSize / 4 // 128 FAT32 entries per sector

	// FAT32 special cluster values (low 28 bits).
	fatFree = 0x00000000
	fatEOC  = 0x0FFFFFFF
	fatBad  = 0x0FFFFFF7

	// Minimum cluster count for a valid FAT32 volume.
	minFAT32Clusters = 65525

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
	errTooSmall = errors.New("fat: device too small for a valid FAT32 volume (need ~34 MiB)")
	errBadSize  = errors.New("fat: device size must be a positive multiple of 512")
)

// geometry derives the FAT32 layout from a device size in bytes.
type geometry struct {
	totalSectors   uint64
	secPerClus     uint32
	fatSizeSectors uint64
	clusters       uint64
	dataStartSec   uint64 // first sector of the data region (cluster 2)
}

func computeGeometry(devSize int64) (geometry, error) {
	var g geometry
	if devSize <= 0 || devSize%sectorSize != 0 {
		return g, errBadSize
	}
	g.totalSectors = uint64(devSize) / sectorSize

	// Pick a cluster size scaling with volume size (mkfs.fat-style thresholds).
	switch {
	case g.totalSectors <= 532480: // <= 260 MiB
		g.secPerClus = 1
	case g.totalSectors <= 16777216: // <= 8 GiB
		g.secPerClus = 8
	default:
		g.secPerClus = 32
	}

	// FAT size: start from the standard approximation, then grow until the FAT
	// can address every data cluster plus the two reserved entries. fsck.fat
	// derives the cluster count from the BPB and rejects a FAT too small for
	// it, so the two must agree exactly.
	tmp := g.totalSectors - reservedSecs
	denom := uint64(g.secPerClus)*entriesPerFATsec + numFATs
	g.fatSizeSectors = (tmp + denom - 1) / denom
	for {
		dataSectors := g.totalSectors - reservedSecs - numFATs*g.fatSizeSectors
		g.clusters = dataSectors / uint64(g.secPerClus)
		if g.fatSizeSectors*entriesPerFATsec >= g.clusters+2 {
			break
		}
		g.fatSizeSectors++
	}
	g.dataStartSec = reservedSecs + numFATs*g.fatSizeSectors

	if g.clusters < minFAT32Clusters {
		return g, errTooSmall
	}
	return g, nil
}

// clusterSizeBytes is the byte size of one cluster.
func (g geometry) clusterSizeBytes() uint32 { return g.secPerClus * sectorSize }

// clusterSector returns the first sector of a data cluster (clusters start at 2).
func (g geometry) clusterSector(cluster uint32) uint64 {
	return g.dataStartSec + uint64(cluster-2)*uint64(g.secPerClus)
}
