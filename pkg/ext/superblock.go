package ext

import (
	"encoding/binary"
	"math/bits"
)

// superblock is the in-memory form of the fields fsforge writes. It is encoded
// into a 1024-byte block; fields we do not use stay zero.
type superblock struct {
	inodesCount     uint32
	blocksCount     uint32
	rBlocksCount    uint32
	freeBlocksCount uint32
	freeInodesCount uint32
	firstDataBlock  uint32
	logBlockSize    uint32
	logFragSize     uint32
	blocksPerGroup  uint32
	fragsPerGroup   uint32
	inodesPerGroup  uint32
	mtime           uint32
	wtime           uint32
	magic           uint16
	state           uint16
	errors          uint16
	revLevel        uint32
	firstIno        uint32
	inodeSize       uint16
	blockGroupNr    uint16
	featureCompat   uint32
	featureIncompat uint32
	featureROCompat uint32
	uuid            [16]byte
	volumeName      [16]byte
}

func (s *superblock) marshal() []byte {
	b := make([]byte, superblockSize)
	le := binary.LittleEndian
	le.PutUint32(b[0:], s.inodesCount)
	le.PutUint32(b[4:], s.blocksCount)
	le.PutUint32(b[8:], s.rBlocksCount)
	le.PutUint32(b[12:], s.freeBlocksCount)
	le.PutUint32(b[16:], s.freeInodesCount)
	le.PutUint32(b[20:], s.firstDataBlock)
	le.PutUint32(b[24:], s.logBlockSize)
	le.PutUint32(b[28:], s.logFragSize)
	le.PutUint32(b[32:], s.blocksPerGroup)
	le.PutUint32(b[36:], s.fragsPerGroup)
	le.PutUint32(b[40:], s.inodesPerGroup)
	le.PutUint32(b[44:], s.mtime)
	le.PutUint32(b[48:], s.wtime)
	le.PutUint16(b[52:], 0)      // s_mnt_count
	le.PutUint16(b[54:], 0xFFFF) // s_max_mnt_count = -1
	le.PutUint16(b[56:], s.magic)
	le.PutUint16(b[58:], s.state)
	le.PutUint16(b[60:], s.errors)
	le.PutUint16(b[62:], 0)       // minor rev
	le.PutUint32(b[64:], s.wtime) // s_lastcheck
	le.PutUint32(b[68:], 0)       // s_checkinterval
	le.PutUint32(b[72:], creatorLinux)
	le.PutUint32(b[76:], s.revLevel)
	le.PutUint16(b[80:], 0) // def_resuid
	le.PutUint16(b[82:], 0) // def_resgid
	le.PutUint32(b[84:], s.firstIno)
	le.PutUint16(b[88:], s.inodeSize)
	le.PutUint16(b[90:], s.blockGroupNr)
	le.PutUint32(b[92:], s.featureCompat)
	le.PutUint32(b[96:], s.featureIncompat)
	le.PutUint32(b[100:], s.featureROCompat)
	copy(b[104:120], s.uuid[:])
	copy(b[120:136], s.volumeName[:])
	return b
}

func parseSuperblock(b []byte) superblock {
	le := binary.LittleEndian
	var s superblock
	s.inodesCount = le.Uint32(b[0:])
	s.blocksCount = le.Uint32(b[4:])
	s.rBlocksCount = le.Uint32(b[8:])
	s.freeBlocksCount = le.Uint32(b[12:])
	s.freeInodesCount = le.Uint32(b[16:])
	s.firstDataBlock = le.Uint32(b[20:])
	s.logBlockSize = le.Uint32(b[24:])
	s.logFragSize = le.Uint32(b[28:])
	s.blocksPerGroup = le.Uint32(b[32:])
	s.fragsPerGroup = le.Uint32(b[36:])
	s.inodesPerGroup = le.Uint32(b[40:])
	s.mtime = le.Uint32(b[44:])
	s.wtime = le.Uint32(b[48:])
	s.magic = le.Uint16(b[56:])
	s.state = le.Uint16(b[58:])
	s.errors = le.Uint16(b[60:])
	s.revLevel = le.Uint32(b[76:])
	s.firstIno = le.Uint32(b[84:])
	s.inodeSize = le.Uint16(b[88:])
	s.blockGroupNr = le.Uint16(b[90:])
	s.featureCompat = le.Uint32(b[92:])
	s.featureIncompat = le.Uint32(b[96:])
	s.featureROCompat = le.Uint32(b[100:])
	copy(s.uuid[:], b[104:120])
	copy(s.volumeName[:], b[120:136])
	return s
}

func (s superblock) blockSize() uint32 { return 1024 << s.logBlockSize }

func logBlockSizeFor(blockSize uint32) uint32 {
	return uint32(bits.TrailingZeros32(blockSize)) - 10
}
