package squashfs

import "encoding/binary"

// superblock holds the squashfs 4.0 superblock fields fsforge sets.
type superblock struct {
	inodes           uint32
	mkfsTime         uint32
	blockSize        uint32
	fragments        uint32
	compression      uint16
	blockLog         uint16
	flags            uint16
	noIDs            uint16
	rootInode        uint64
	bytesUsed        uint64
	idTableStart     uint64
	xattrTableStart  uint64
	inodeTableStart  uint64
	dirTableStart    uint64
	fragTableStart   uint64
	lookupTableStart uint64
}

func (s superblock) marshal() []byte {
	b := make([]byte, superblockSize)
	le := binary.LittleEndian
	le.PutUint32(b[0:], magic)
	le.PutUint32(b[4:], s.inodes)
	le.PutUint32(b[8:], s.mkfsTime)
	le.PutUint32(b[12:], s.blockSize)
	le.PutUint32(b[16:], s.fragments)
	le.PutUint16(b[20:], s.compression)
	le.PutUint16(b[22:], s.blockLog)
	le.PutUint16(b[24:], s.flags)
	le.PutUint16(b[26:], s.noIDs)
	le.PutUint16(b[28:], versionMajor)
	le.PutUint16(b[30:], versionMinor)
	le.PutUint64(b[32:], s.rootInode)
	le.PutUint64(b[40:], s.bytesUsed)
	le.PutUint64(b[48:], s.idTableStart)
	le.PutUint64(b[56:], s.xattrTableStart)
	le.PutUint64(b[64:], s.inodeTableStart)
	le.PutUint64(b[72:], s.dirTableStart)
	le.PutUint64(b[80:], s.fragTableStart)
	le.PutUint64(b[88:], s.lookupTableStart)
	return b
}

// inodeRef packs a metadata-block start and an in-block offset into the 48-bit
// reference squashfs uses to locate inodes and directory listings.
func inodeRef(blockStart uint32, offset uint16) uint64 {
	return uint64(blockStart)<<16 | uint64(offset)
}
