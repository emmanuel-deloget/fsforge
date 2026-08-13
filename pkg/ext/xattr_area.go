package ext

import "encoding/binary"

// encodeXattrIbody renders the attributes that fit in the space left over
// inside a large inode: everything past i_extra_isize, headed by the magic.
// Value offsets there are measured from the first entry, not from the inode.
//
// It reports ok=false when the attributes do not fit, which is the caller's cue
// to spend a block instead — not an error, just a different home.
func encodeXattrIbody(entries []xattrEntry, space int) ([]byte, bool) {
	if len(entries) == 0 {
		return nil, true
	}
	need := xattrIbodyHeaderSize + xattrAreaSize(entries)
	if need > space {
		return nil, false
	}
	buf := make([]byte, space)
	binary.LittleEndian.PutUint32(buf[0:], xattrMagic)
	if err := encodeXattrArea(buf[xattrIbodyHeaderSize:], entries, 0); err != nil {
		return nil, false
	}
	return buf, true
}

// decodeXattrIbody reads back what encodeXattrIbody wrote. area is the inode
// bytes from the end of the extra area to the end of the inode.
func decodeXattrIbody(area []byte) map[string][]byte {
	if len(area) < xattrIbodyHeaderSize+4 {
		return nil
	}
	if binary.LittleEndian.Uint32(area[0:]) != xattrMagic {
		return nil
	}
	entries := area[xattrIbodyHeaderSize:]
	return decodeXattrArea(entries, entries, 0)
}

// encodeXattrBlock renders one whole block: the 32-byte header, the entries,
// and the values packed against the end. Value offsets here are measured from
// the start of the block.
func encodeXattrBlock(entries []xattrEntry, blockSize int) ([]byte, error) {
	buf := make([]byte, blockSize)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], xattrMagic)
	le.PutUint32(buf[4:], 1) // h_refcount: not shared between inodes
	le.PutUint32(buf[8:], 1) // h_blocks: always one
	// h_hash (12) and h_checksum (16) are filled below and by the caller.

	if err := encodeXattrArea(buf[xattrBlockHeaderSize:], entries, xattrBlockHeaderSize); err != nil {
		return nil, err
	}
	hashXattrBlock(buf)
	return buf, nil
}

// decodeXattrBlock reads a whole xattr block.
func decodeXattrBlock(block []byte) map[string][]byte {
	if len(block) < xattrBlockHeaderSize+4 {
		return nil
	}
	if binary.LittleEndian.Uint32(block[0:]) != xattrMagic {
		return nil
	}
	return decodeXattrArea(block[xattrBlockHeaderSize:], block, 0)
}

// hashXattrBlock fills each entry's e_hash and then the header's h_hash, the
// way fs/ext4/xattr.c does. e2fsck checks these, and an image whose hashes are
// zero is one e2fsck offers to repair — which is the same as saying it is
// wrong.
func hashXattrBlock(block []byte) {
	le := binary.LittleEndian
	var blockHash uint32

	for off := xattrBlockHeaderSize; off+xattrEntrySize <= len(block); {
		nameLen := int(block[off])
		index := block[off+1]
		if nameLen == 0 && index == 0 {
			break
		}
		valOff := int(le.Uint16(block[off+2:]))
		valSize := int(le.Uint32(block[off+8:]))
		name := block[off+xattrEntrySize : off+xattrEntrySize+nameLen]

		var h uint32
		for _, c := range name {
			h = (h << nameHashShift) ^ (h >> (32 - nameHashShift)) ^ uint32(c)
		}
		// The value is hashed as little-endian words over its padded length.
		if valSize > 0 && valOff+align4(valSize) <= len(block) {
			for n := align4(valSize) / 4; n > 0; n-- {
				h = (h << valueHashShift) ^ (h >> (32 - valueHashShift)) ^
					le.Uint32(block[valOff:])
				valOff += 4
			}
		}
		le.PutUint32(block[off+12:], h)

		blockHash = (blockHash << blockHashShift) ^ (blockHash >> (32 - blockHashShift)) ^ h
		off += entryLen(nameLen)
	}
	le.PutUint32(block[12:], blockHash)
}
