package ext

import "encoding/binary"

// dentry is one directory entry to be written.
type dentry struct {
	ino   uint32
	name  string
	ftype byte
}

func dentryLen(nameLen int) int {
	// 8-byte header + name, rounded up to 4.
	return (8 + nameLen + 3) &^ 3
}

// buildDirBlocks lays out entries into one or more blocks. Entries never span a
// block boundary; the last entry in each block has its rec_len extended to the
// block end, as required by the on-disk format.
func buildDirBlocks(entries []dentry, blockSize uint32) [][]byte {
	bs := int(blockSize)
	var blocks [][]byte
	cur := make([]byte, bs)
	off := 0
	lastOff := 0

	flush := func() {
		// Extend the last entry to fill the block.
		if off > 0 {
			binary.LittleEndian.PutUint16(cur[lastOff+4:], uint16(bs-lastOff))
		}
		blocks = append(blocks, cur)
		cur = make([]byte, bs)
		off, lastOff = 0, 0
	}

	for _, e := range entries {
		need := dentryLen(len(e.name))
		if off+need > bs {
			flush()
		}
		putDentry(cur[off:], e, need)
		lastOff = off
		off += need
	}
	flush()
	return blocks
}

func putDentry(b []byte, e dentry, recLen int) {
	le := binary.LittleEndian
	le.PutUint32(b[0:], e.ino)
	le.PutUint16(b[4:], uint16(recLen))
	b[6] = byte(len(e.name))
	b[7] = e.ftype
	copy(b[8:], e.name)
}

// parseDirBlock walks one directory block, calling fn for each live entry.
func parseDirBlock(b []byte, fn func(ino uint32, name string, ftype byte)) {
	le := binary.LittleEndian
	for off := 0; off+8 <= len(b); {
		ino := le.Uint32(b[off:])
		recLen := int(le.Uint16(b[off+4:]))
		if recLen < 8 {
			break
		}
		nameLen := int(b[off+6])
		ftype := b[off+7]
		if ino != 0 && off+8+nameLen <= len(b) {
			fn(ino, string(b[off+8:off+8+nameLen]), ftype)
		}
		off += recLen
	}
}
