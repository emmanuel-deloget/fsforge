package erofs

import "errors"

// superblock holds the EROFS fields fsforge sets. Everything beyond
// feature_incompat (offset 80) is reserved in the v1 layout and left zero,
// which a modern kernel reads as "no extra devices, no compression configs,
// dir block size == block size" — exactly what an uncompressed image wants.
type superblock struct {
	rootNid      uint16
	inos         uint64
	buildTime    uint64
	buildNsec    uint32
	blocks       uint32
	metaBlkaddr  uint32
	xattrBlkaddr uint32
	uuid         [16]byte
	volumeName   [16]byte
}

func (s superblock) marshal() []byte {
	b := make([]byte, superSize)
	le.PutUint32(b[0:], superMagic)
	// b[4:8]  checksum       = 0 (EROFS_FEATURE_COMPAT_SB_CHKSUM not set)
	// b[8:12] feature_compat = 0
	b[12] = blkSizeBits
	// b[13] sb_extslots = 0 -> superblock is 128 bytes
	le.PutUint16(b[14:], s.rootNid)
	le.PutUint64(b[16:], s.inos)
	le.PutUint64(b[24:], s.buildTime)
	le.PutUint32(b[32:], s.buildNsec)
	le.PutUint32(b[36:], s.blocks)
	le.PutUint32(b[40:], s.metaBlkaddr)
	le.PutUint32(b[44:], s.xattrBlkaddr)
	copy(b[48:64], s.uuid[:])
	copy(b[64:80], s.volumeName[:])
	// b[80:84] feature_incompat = 0; the remaining reserved bytes stay zero.
	return b
}

var errBadMagic = errors.New("erofs: bad superblock magic")

func parseSuperblock(b []byte) (superblock, error) {
	var s superblock
	if le.Uint32(b[0:]) != superMagic {
		return s, errBadMagic
	}
	s.rootNid = le.Uint16(b[14:])
	s.inos = le.Uint64(b[16:])
	s.buildTime = le.Uint64(b[24:])
	s.buildNsec = le.Uint32(b[32:])
	s.blocks = le.Uint32(b[36:])
	s.metaBlkaddr = le.Uint32(b[40:])
	s.xattrBlkaddr = le.Uint32(b[44:])
	copy(s.uuid[:], b[48:64])
	copy(s.volumeName[:], b[64:80])
	return s, nil
}
