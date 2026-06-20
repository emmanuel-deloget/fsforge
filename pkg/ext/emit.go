package ext

import (
	"encoding/binary"
	"io"
	"sort"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// --- allocation wrappers that mirror usage so we can serialise bitmaps without
// depending on the allocator's internals ---

func (l *layouter) mark(start, n uint64) {
	for i := start; i < start+n && i < l.geo.totalBlocks; i++ {
		l.used[i] = true
	}
}

func (l *layouter) reserve(start, n uint64) {
	l.mark(start, n)
	_ = l.al.Reserve(start, n)
}

func (l *layouter) allocOne() (uint64, error) {
	s, err := l.al.Alloc(1)
	if err != nil {
		return 0, err
	}
	l.mark(s, 1)
	return s, nil
}

// allocContiguous allocates n contiguous blocks and returns their numbers. n==0
// returns an empty slice.
func (l *layouter) allocContiguous(n uint64) ([]uint64, error) {
	if n == 0 {
		return nil, nil
	}
	start, err := l.al.Alloc(n)
	if err != nil {
		return nil, err
	}
	l.mark(start, n)
	out := make([]uint64, n)
	for i := range out {
		out[i] = start + uint64(i)
	}
	return out, nil
}

// --- device writes ---

func (l *layouter) writeBlock(block uint64, data []byte) {
	_, _ = l.dev.WriteAt(data, int64(block)*int64(l.geo.blockSize))
}

func (l *layouter) writeContent(src interface {
	io.ReaderAt
	Size() int64
}, blocks []uint64, size uint64) error {
	bs := uint64(l.geo.blockSize)
	for i, blk := range blocks {
		off := uint64(i) * bs
		n := bs
		if off+n > size {
			n = size - off
		}
		buf := make([]byte, bs) // zero-padded tail
		if _, err := src.ReadAt(buf[:n], int64(off)); err != nil && err != io.EOF {
			return err
		}
		l.writeBlock(blk, buf)
	}
	return nil
}

func (l *layouter) writeBytes(data []byte, blocks []uint64) error {
	bs := int(l.geo.blockSize)
	for i, blk := range blocks {
		buf := make([]byte, bs)
		copy(buf, data[i*bs:min((i+1)*bs, len(data))])
		l.writeBlock(blk, buf)
	}
	return nil
}

// --- metadata emission ---

func (l *layouter) writeInodeTables() error {
	g := l.geo
	for gr := uint64(0); gr < g.numGroups; gr++ {
		_, _, table, _ := g.groupLayout(gr)
		buf := make([]byte, g.inodeTableBlocks*uint64(g.blockSize))
		for i := uint64(0); i < g.inodesPerGroup; i++ {
			ino := gr*g.inodesPerGroup + i + 1
			in, ok := l.inodes[uint32(ino)]
			if !ok {
				continue // reserved/free inode: left zero
			}
			slot := i * uint64(g.inodeSize)
			in.marshalInto(buf[slot : slot+uint64(g.inodeSize)])
		}
		l.writeBlocks(table, buf)
	}
	return nil
}

func (l *layouter) writeBlocks(start uint64, data []byte) {
	_, _ = l.dev.WriteAt(data, int64(start)*int64(l.geo.blockSize))
}

func (l *layouter) writeBitmaps() error {
	g := l.geo
	for gr := uint64(0); gr < g.numGroups; gr++ {
		bbm, ibm, _, _ := g.groupLayout(gr)

		// Block bitmap: bit n -> block groupStart+n.
		blockBuf := make([]byte, g.blockSize)
		start := g.groupStart(gr)
		inGroup := g.blocksInGroup(gr)
		for n := uint64(0); n < g.blocksPerGroup; n++ {
			abs := start + n
			if n >= inGroup || l.used[abs] {
				setBit(blockBuf, n)
			}
		}
		l.writeBlock(bbm, blockBuf)

		// Inode bitmap: bit m -> inode gr*ipg+m+1.
		inodeBuf := make([]byte, g.blockSize)
		for m := uint64(0); m < uint64(g.blockSize)*8; m++ {
			if m >= g.inodesPerGroup {
				setBit(inodeBuf, m)
				continue
			}
			ino := gr*g.inodesPerGroup + m + 1
			if l.usedInos[uint32(ino)] {
				setBit(inodeBuf, m)
			}
		}
		l.writeBlock(ibm, inodeBuf)
	}
	return nil
}

func (l *layouter) writeDescriptorsAndSuperblocks() error {
	g := l.geo

	// Build descriptors and accumulate totals.
	descs := make([]groupDesc, g.numGroups)
	var totalFreeBlocks, totalFreeInodes uint64
	for gr := uint64(0); gr < g.numGroups; gr++ {
		bbm, ibm, table, _ := g.groupLayout(gr)
		freeBlocks := g.blocksInGroup(gr) - l.usedBlocksInGroup(gr)
		freeInodes := g.inodesPerGroup - l.usedInodesInGroup(gr)
		descs[gr] = groupDesc{
			blockBitmap: uint32(bbm),
			inodeBitmap: uint32(ibm),
			inodeTable:  uint32(table),
			freeBlocks:  uint16(freeBlocks),
			freeInodes:  uint16(freeInodes),
			usedDirs:    uint16(l.dirsInGroup(gr)),
		}
		totalFreeBlocks += freeBlocks
		totalFreeInodes += freeInodes
	}

	gdt := make([]byte, g.gdtBlocks*uint64(g.blockSize))
	for i, d := range descs {
		d.marshalInto(gdt[i*descSize:])
	}

	now := uint32(l.deps.Clock.Now().Unix())
	sb := superblock{
		inodesCount:     uint32(g.inodesCount),
		blocksCount:     uint32(g.totalBlocks),
		rBlocksCount:    uint32(g.totalBlocks * 5 / 100),
		freeBlocksCount: uint32(totalFreeBlocks),
		freeInodesCount: uint32(totalFreeInodes),
		firstDataBlock:  uint32(g.firstDataBlock),
		logBlockSize:    logBlockSizeFor(g.blockSize),
		logFragSize:     logBlockSizeFor(g.blockSize),
		blocksPerGroup:  uint32(g.blocksPerGroup),
		fragsPerGroup:   uint32(g.blocksPerGroup),
		inodesPerGroup:  uint32(g.inodesPerGroup),
		mtime:           0,
		wtime:           now,
		magic:           magic,
		state:           stateClean,
		errors:          errorsContinue,
		revLevel:        dynamicRev,
		firstIno:        firstIno,
		inodeSize:       uint16(g.inodeSize),
		featureIncompat: l.featIncompat,
		featureROCompat: l.featRoCompat,
	}
	if g.inodeSize > goodOldInodeSize {
		sb.minExtraIsize = extraISize
		sb.wantExtraIsize = extraISize
	}
	uuid := l.deps.UUID.UUID()
	copy(sb.uuid[:], uuid[:])
	copy(sb.volumeName[:], l.params.Label)

	// Write primary + every backup (sparse_super) copy.
	for gr := uint64(0); gr < g.numGroups; gr++ {
		if !hasSuperblock(gr) {
			continue
		}
		sb.blockGroupNr = uint16(gr)
		sbBytes := sb.marshal()
		if gr == 0 {
			_, _ = l.dev.WriteAt(sbBytes, superblockOffset)
			l.writeBlocks(g.firstDataBlock+1, gdt)
		} else {
			start := g.groupStart(gr)
			l.writeBlock(start, sbBytes)
			l.writeBlocks(start+1, gdt)
		}
	}
	return nil
}

// --- per-group counting helpers ---

func (l *layouter) usedBlocksInGroup(gr uint64) uint64 {
	start := l.geo.groupStart(gr)
	in := l.geo.blocksInGroup(gr)
	var c uint64
	for n := uint64(0); n < in; n++ {
		if l.used[start+n] {
			c++
		}
	}
	return c
}

func (l *layouter) usedInodesInGroup(gr uint64) uint64 {
	var c uint64
	for m := uint64(0); m < l.geo.inodesPerGroup; m++ {
		if l.usedInos[uint32(gr*l.geo.inodesPerGroup+m+1)] {
			c++
		}
	}
	return c
}

func (l *layouter) dirsInGroup(gr uint64) uint64 {
	var c uint64
	for _, ino := range l.inoOf {
		if l.inodes[ino] != nil && l.inodes[ino].mode&0xF000 == modeDir {
			if (uint64(ino)-1)/l.geo.inodesPerGroup == gr {
				c++
			}
		}
	}
	return c
}

// --- small helpers ---

func setBit(b []byte, i uint64) { b[i/8] |= 1 << (i % 8) }

func putU32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }

func sortedChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func findChild(n *image.Node, name string) *image.Entry {
	for i := range n.Children {
		if n.Children[i].Name == name {
			return &n.Children[i]
		}
	}
	return nil
}
