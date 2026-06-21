package exfat

import "errors"

const (
	bytesPerSector      = 512
	bytesPerSectorShift = 9
	bootRegionSectors   = 24 // main (12) + backup (12)
	dirEntrySize        = 32
)

var (
	errBadSize  = errors.New("exfat: device size must be a positive multiple of 512")
	errTooSmall = errors.New("exfat: device too small for an exFAT volume")
)

// geometry derives the exFAT layout from a device size in bytes.
type geometry struct {
	totalSectors     uint64
	spcShift         uint32 // sectors-per-cluster shift
	secPerClus       uint32
	fatOffset        uint64 // sector
	fatLength        uint64 // sectors
	clusterHeapStart uint64 // sector
	clusterCount     uint64
}

func computeGeometry(devSize int64) (geometry, error) {
	var g geometry
	if devSize <= 0 || devSize%bytesPerSector != 0 {
		return g, errBadSize
	}
	g.totalSectors = uint64(devSize) / bytesPerSector

	switch {
	case g.totalSectors <= 1<<20: // <= 512 MiB -> 4 KiB clusters
		g.spcShift = 3
	case g.totalSectors <= 1<<24: // <= 8 GiB -> 32 KiB
		g.spcShift = 6
	default: // -> 128 KiB
		g.spcShift = 8
	}
	g.secPerClus = 1 << g.spcShift
	g.fatOffset = bootRegionSectors

	// Solve FAT length and cluster count together.
	g.fatLength = 1
	for {
		heap := align(g.fatOffset+g.fatLength, uint64(g.secPerClus))
		if heap >= g.totalSectors {
			return g, errTooSmall
		}
		g.clusterHeapStart = heap
		g.clusterCount = (g.totalSectors - heap) / uint64(g.secPerClus)
		needBytes := (g.clusterCount + 2) * 4
		needSecs := align(needBytes, bytesPerSector) / bytesPerSector
		if needSecs <= g.fatLength {
			break
		}
		g.fatLength = needSecs
	}
	if g.clusterCount < 1 {
		return g, errTooSmall
	}
	return g, nil
}

func (g geometry) clusterBytes() uint64 { return uint64(g.secPerClus) * bytesPerSector }

// clusterSector returns the first sector of a data cluster (clusters start at 2).
func (g geometry) clusterSector(cluster uint32) uint64 {
	return g.clusterHeapStart + uint64(cluster-2)*uint64(g.secPerClus)
}

func align(v, to uint64) uint64 { return (v + to - 1) / to * to }
