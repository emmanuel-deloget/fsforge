package ext

import (
	"errors"
	"math/bits"
)

// geometry holds the derived sizes that drive the whole layout. It is a pure
// function of the device size and the requested block size, which keeps the
// layout deterministic.
type geometry struct {
	blockSize        uint32
	totalBlocks      uint64
	firstDataBlock   uint64
	blocksPerGroup   uint64
	inodesPerGroup   uint64
	inodeSize        uint32
	inodesPerBlock   uint64
	inodesCount      uint64
	numGroups        uint64
	gdtBlocks        uint64
	inodeTableBlocks uint64 // per group
}

var (
	errBlockSize  = errors.New("ext: block size must be a power of two in [1024, 65536]")
	errTooSmall   = errors.New("ext: device too small for an ext2 filesystem")
	errDeviceSize = errors.New("ext: device size must be a multiple of the block size")
)

func ceilDiv(a, b uint64) uint64 { return (a + b - 1) / b }

func roundUp(a, mult uint64) uint64 { return ceilDiv(a, mult) * mult }

// computeGeometry derives the layout for a device of devSize bytes. A blockSize
// of 0 selects the default.
func computeGeometry(devSize int64, blockSize uint32) (geometry, error) {
	var g geometry
	if blockSize == 0 {
		blockSize = defaultBlockSize
	}
	if blockSize < 1024 || blockSize > 65536 || bits.OnesCount32(blockSize) != 1 {
		return g, errBlockSize
	}
	if devSize <= 0 || uint64(devSize)%uint64(blockSize) != 0 {
		return g, errDeviceSize
	}

	g.blockSize = blockSize
	g.totalBlocks = uint64(devSize) / uint64(blockSize)
	if blockSize == 1024 {
		g.firstDataBlock = 1
	}
	g.blocksPerGroup = uint64(blockSize) * 8
	g.inodeSize = goodOldInodeSize
	g.inodesPerBlock = uint64(blockSize) / uint64(g.inodeSize)

	g.numGroups = ceilDiv(g.totalBlocks-g.firstDataBlock, g.blocksPerGroup)
	g.gdtBlocks = ceilDiv(g.numGroups*descSize, uint64(blockSize))

	// Inode count from the bytes-per-inode heuristic, then spread across groups.
	ic := uint64(devSize) / bytesPerInode
	ipg := ceilDiv(ic, g.numGroups)
	if ipg < 16 {
		ipg = 16
	}
	ipg = roundUp(ipg, 8)
	if max := g.blocksPerGroup; ipg > max { // inode bitmap holds blocksPerGroup bits
		ipg = max
	}
	g.inodesPerGroup = ipg
	g.inodesCount = ipg * g.numGroups
	g.inodeTableBlocks = ceilDiv(ipg*uint64(g.inodeSize), uint64(blockSize))

	// Sanity: group 0 must hold the reserved inodes and lost+found.
	if g.inodesPerGroup < firstIno {
		return g, errTooSmall
	}
	if g.totalBlocks < g.overhead(0)+8 {
		return g, errTooSmall
	}
	return g, nil
}

// hasSuperblock reports whether group gr carries a superblock + GDT backup under
// the sparse_super rule: group 0, and groups that are powers of 3, 5 or 7.
func hasSuperblock(gr uint64) bool {
	if gr == 0 {
		return true
	}
	return isPowerOf(gr, 3) || isPowerOf(gr, 5) || isPowerOf(gr, 7)
}

func isPowerOf(n, base uint64) bool {
	if n == 0 {
		return false
	}
	for n%base == 0 {
		n /= base
	}
	return n == 1
}

// groupStart is the first block number of group gr.
func (g geometry) groupStart(gr uint64) uint64 {
	return g.firstDataBlock + gr*g.blocksPerGroup
}

// overhead is the number of metadata blocks at the start of group gr (superblock
// + GDT when present, then the two bitmaps and the inode table).
func (g geometry) overhead(gr uint64) uint64 {
	o := uint64(2) + g.inodeTableBlocks // block bitmap + inode bitmap + inode table
	if hasSuperblock(gr) {
		o += 1 + g.gdtBlocks
	}
	return o
}

// groupLayout returns the absolute block numbers of a group's metadata and the
// first usable data block.
func (g geometry) groupLayout(gr uint64) (blockBitmap, inodeBitmap, inodeTable, firstData uint64) {
	b := g.groupStart(gr)
	if hasSuperblock(gr) {
		b += 1 + g.gdtBlocks
	}
	blockBitmap = b
	b++
	inodeBitmap = b
	b++
	inodeTable = b
	b += g.inodeTableBlocks
	firstData = b
	return
}

// blocksInGroup is the number of real blocks covered by group gr's block bitmap
// (the last group may be short).
func (g geometry) blocksInGroup(gr uint64) uint64 {
	start := g.groupStart(gr)
	end := start + g.blocksPerGroup
	if end > g.totalBlocks {
		end = g.totalBlocks
	}
	return end - start
}

// inodesInGroup is the number of inodes in group gr (constant across groups).
func (g geometry) inodesInGroup(gr uint64) uint64 { return g.inodesPerGroup }
