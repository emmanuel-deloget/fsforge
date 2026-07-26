package ext

import (
	"encoding/binary"
	"errors"
)

// ext4 extents. A small object gets a depth-0 tree: the extent header plus up to
// four leaf extents fit in the 60-byte i_block area. Anything larger needs real
// nodes — one extent addresses at most extentMaxLen blocks, and per-group
// metadata caps a free run at one block group, so a big file is spread over many
// extents. Those leaves are written to blocks of their own and covered by index
// entries, level by level, until the root fits inline.
var (
	errExtentDepth = errors.New("ext: unexpected extent tree depth")
	errBadExtent   = errors.New("ext: bad extent header magic")
)

const (
	extentHeaderSize = 12 // struct ext4_extent_header
	extentEntrySize  = 12 // struct ext4_extent / ext4_extent_idx
	inlineExtents    = 4  // entries that fit in i_block beside the header
	maxExtentDepth   = 5  // the kernel refuses deeper trees
)

type extentLeaf struct {
	logical uint32
	length  uint16
	start   uint64
}

// extentIndex points at a child node covering logical blocks from logical on.
type extentIndex struct {
	logical uint32
	child   uint64
}

func contiguousRuns(data []uint64) []extentLeaf {
	var runs []extentLeaf
	logical := uint32(0)
	for i := 0; i < len(data); {
		j := i + 1
		for j < len(data) && data[j] == data[j-1]+1 {
			j++
		}
		runLen := j - i
		phys := data[i]
		// Split into extentMaxLen-sized leaves.
		for runLen > 0 {
			clen := runLen
			if clen > extentMaxLen {
				clen = extentMaxLen
			}
			runs = append(runs, extentLeaf{logical: logical, length: uint16(clen), start: phys})
			logical += uint32(clen)
			phys += uint64(clen)
			runLen -= clen
		}
		i = j
	}
	return runs
}

// entriesPerNode is how many entries a block-sized extent node holds after its
// header.
func entriesPerNode(blockSize uint32) int {
	return (int(blockSize) - extentHeaderSize) / extentEntrySize
}

func putExtentHeader(b []byte, entries, max int, depth uint16) {
	le := binary.LittleEndian
	le.PutUint16(b[0:], extentMagic)
	le.PutUint16(b[2:], uint16(entries))
	le.PutUint16(b[4:], uint16(max))
	le.PutUint16(b[6:], depth)
	le.PutUint32(b[8:], 0) // eh_generation
}

func putExtentLeaf(b []byte, e extentLeaf) {
	le := binary.LittleEndian
	le.PutUint32(b[0:], e.logical)
	le.PutUint16(b[4:], e.length)
	le.PutUint16(b[6:], uint16(e.start>>32))
	le.PutUint32(b[8:], uint32(e.start))
}

func putExtentIndex(b []byte, e extentIndex) {
	le := binary.LittleEndian
	le.PutUint32(b[0:], e.logical)
	le.PutUint32(b[4:], uint32(e.child))
	le.PutUint16(b[8:], uint16(e.child>>32))
	le.PutUint16(b[10:], 0) // ei_unused
}

// buildExtentTree encodes the extent tree for data and returns the 60-byte
// i_block image of its root, writing any node blocks it needs along the way.
func (l *layouter) buildExtentTree(data []uint64) ([]byte, error) {
	leaves := contiguousRuns(data)
	if len(leaves) <= inlineExtents {
		root := make([]byte, totalIBlocks*4)
		putExtentHeader(root, len(leaves), inlineExtents, 0)
		for i, e := range leaves {
			putExtentLeaf(root[extentHeaderSize+i*extentEntrySize:], e)
		}
		return root, nil
	}
	perNode := entriesPerNode(l.geo.blockSize)
	idx, err := l.emitLeafNodes(leaves, perNode)
	if err != nil {
		return nil, err
	}
	for depth := uint16(1); ; depth++ {
		if depth > maxExtentDepth {
			return nil, errExtentDepth
		}
		if len(idx) <= inlineExtents {
			root := make([]byte, totalIBlocks*4)
			putExtentHeader(root, len(idx), inlineExtents, depth)
			for i, e := range idx {
				putExtentIndex(root[extentHeaderSize+i*extentEntrySize:], e)
			}
			return root, nil
		}
		if idx, err = l.emitIndexNodes(idx, perNode, depth); err != nil {
			return nil, err
		}
	}
}

// emitLeafNodes packs leaves into extent nodes of their own and returns the index
// entries covering them.
func (l *layouter) emitLeafNodes(leaves []extentLeaf, perNode int) ([]extentIndex, error) {
	var idx []extentIndex
	for off := 0; off < len(leaves); off += perNode {
		group := leaves[off:min(off+perNode, len(leaves))]
		blk, err := l.writeExtentNode(len(group), perNode, 0, func(dst []byte, i int) {
			putExtentLeaf(dst, group[i])
		})
		if err != nil {
			return nil, err
		}
		idx = append(idx, extentIndex{logical: group[0].logical, child: blk})
	}
	return idx, nil
}

// emitIndexNodes packs index entries into nodes of the given depth and returns
// the index entries of the level above.
func (l *layouter) emitIndexNodes(entries []extentIndex, perNode int, depth uint16) ([]extentIndex, error) {
	var idx []extentIndex
	for off := 0; off < len(entries); off += perNode {
		group := entries[off:min(off+perNode, len(entries))]
		blk, err := l.writeExtentNode(len(group), perNode, depth, func(dst []byte, i int) {
			putExtentIndex(dst, group[i])
		})
		if err != nil {
			return nil, err
		}
		idx = append(idx, extentIndex{logical: group[0].logical, child: blk})
	}
	return idx, nil
}

// writeExtentNode allocates one block, fills it with an extent node holding n
// entries encoded by put, and returns the block number.
func (l *layouter) writeExtentNode(n, perNode int, depth uint16, put func(dst []byte, i int)) (uint64, error) {
	buf := make([]byte, l.geo.blockSize)
	putExtentHeader(buf, n, perNode, depth)
	for i := 0; i < n; i++ {
		put(buf[extentHeaderSize+i*extentEntrySize:], i)
	}
	blk, err := l.allocMetaBlock()
	if err != nil {
		return 0, err
	}
	l.writeBlock(blk, buf)
	return blk, nil
}

// parseExtents walks an extent node (recursing through index nodes) and appends
// the data block numbers. read reads a child block when depth > 0.
func parseExtents(node []byte, read func(uint64) ([]byte, error)) ([]uint64, error) {
	le := binary.LittleEndian
	if le.Uint16(node[0:]) != extentMagic {
		return nil, errBadExtent
	}
	entries := int(le.Uint16(node[2:]))
	depth := le.Uint16(node[6:])
	if depth > maxExtentDepth {
		return nil, errExtentDepth
	}
	var out []uint64
	for i := 0; i < entries; i++ {
		o := extentHeaderSize + i*extentEntrySize
		if o+extentEntrySize > len(node) {
			break
		}
		if depth == 0 {
			length := uint64(le.Uint16(node[o+4:]))
			if length > extentMaxLen {
				length -= extentMaxLen // uninitialised extent
			}
			start := uint64(le.Uint16(node[o+6:]))<<32 | uint64(le.Uint32(node[o+8:]))
			for k := uint64(0); k < length; k++ {
				out = append(out, start+k)
			}
		} else {
			child := uint64(le.Uint16(node[o+8:]))<<32 | uint64(le.Uint32(node[o+4:]))
			buf, err := read(child)
			if err != nil {
				return nil, err
			}
			sub, err := parseExtents(buf, read)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		}
	}
	return out, nil
}
