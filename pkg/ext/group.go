package ext

import "encoding/binary"

// groupDesc is one entry of the block group descriptor table (32 bytes).
type groupDesc struct {
	blockBitmap uint32
	inodeBitmap uint32
	inodeTable  uint32
	freeBlocks  uint16
	freeInodes  uint16
	usedDirs    uint16
}

func (d groupDesc) marshalInto(b []byte) {
	le := binary.LittleEndian
	le.PutUint32(b[0:], d.blockBitmap)
	le.PutUint32(b[4:], d.inodeBitmap)
	le.PutUint32(b[8:], d.inodeTable)
	le.PutUint16(b[12:], d.freeBlocks)
	le.PutUint16(b[14:], d.freeInodes)
	le.PutUint16(b[16:], d.usedDirs)
	// bytes 18..32 stay zero (pad + reserved)
}

func parseGroupDesc(b []byte) groupDesc {
	le := binary.LittleEndian
	return groupDesc{
		blockBitmap: le.Uint32(b[0:]),
		inodeBitmap: le.Uint32(b[4:]),
		inodeTable:  le.Uint32(b[8:]),
		freeBlocks:  le.Uint16(b[12:]),
		freeInodes:  le.Uint16(b[14:]),
		usedDirs:    le.Uint16(b[16:]),
	}
}
