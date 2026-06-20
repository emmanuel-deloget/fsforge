package ext

import (
	"encoding/binary"
	"errors"
)

// ext4 extents. fsforge writes only a depth-0 (inline) extent tree: the extent
// header plus up to four leaf extents fit in the 60-byte i_block area. Because
// the allocator hands out one contiguous run per object, files and directories
// are usually a single extent; runs longer than extentMaxLen are split.
var (
	errExtentDepth   = errors.New("ext: unexpected extent tree depth")
	errTooManyExtent = errors.New("ext: file needs more than 4 inline extents")
	errBadExtent     = errors.New("ext: bad extent header magic")
)

type extentLeaf struct {
	logical uint32
	length  uint16
	start   uint64
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

// buildExtentsInline encodes a depth-0 extent tree into the 60-byte i_block area.
func buildExtentsInline(data []uint64) ([]byte, error) {
	leaves := contiguousRuns(data)
	if len(leaves) > 4 {
		return nil, errTooManyExtent
	}
	raw := make([]byte, totalIBlocks*4) // 60 bytes
	le := binary.LittleEndian
	le.PutUint16(raw[0:], extentMagic)
	le.PutUint16(raw[2:], uint16(len(leaves)))
	le.PutUint16(raw[4:], 4) // eh_max: four entries fit inline
	le.PutUint16(raw[6:], 0) // eh_depth: leaf node
	for i, e := range leaves {
		o := 12 + i*12
		le.PutUint32(raw[o:], e.logical)
		le.PutUint16(raw[o+4:], e.length)
		le.PutUint16(raw[o+6:], uint16(e.start>>32))
		le.PutUint32(raw[o+8:], uint32(e.start))
	}
	return raw, nil
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
	var out []uint64
	for i := 0; i < entries; i++ {
		o := 12 + i*12
		if o+12 > len(node) {
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
	if depth > 8 {
		return nil, errExtentDepth
	}
	return out, nil
}
