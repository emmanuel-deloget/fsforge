package erofs

import "io/fs"

// Unix st_mode constants. Go's fs.FileMode encodes type bits differently, so
// engines translate at the boundary.
const (
	sIFMT   = 0o170000
	sIFSOCK = 0o140000
	sIFLNK  = 0o120000
	sIFREG  = 0o100000
	sIFBLK  = 0o060000
	sIFDIR  = 0o040000
	sIFCHR  = 0o020000
	sIFIFO  = 0o010000

	sISUID = 0o4000
	sISGID = 0o2000
	sISVTX = 0o1000
)

// dinode is the subset of an EROFS extended inode (erofs_inode_extended)
// fsforge writes. i_format is always FLAT_PLAIN extended and i_xattr_icount is
// always zero.
type dinode struct {
	mode  uint16
	size  uint64
	union uint32 // i_u: raw_blkaddr for FLAT_PLAIN data, or rdev for devices
	ino   uint32
	uid   uint32
	gid   uint32
	mtime uint64
	nsec  uint32
	nlink uint32
}

func (in dinode) marshal() []byte {
	b := make([]byte, inodeExtendedSize)
	le.PutUint16(b[0:], formatExtendedFlatPlain)
	// b[2:4] i_xattr_icount = 0
	le.PutUint16(b[4:], in.mode)
	// b[6:8] i_reserved = 0
	le.PutUint64(b[8:], in.size)
	le.PutUint32(b[16:], in.union)
	le.PutUint32(b[20:], in.ino)
	le.PutUint32(b[24:], in.uid)
	le.PutUint32(b[28:], in.gid)
	le.PutUint64(b[32:], in.mtime)
	le.PutUint32(b[40:], in.nsec)
	le.PutUint32(b[44:], in.nlink)
	// b[48:64] i_reserved2 = 0
	return b
}

// modeToUnix turns a Go file mode into the 16-bit Unix st_mode EROFS stores.
func modeToUnix(m fs.FileMode) uint16 {
	v := uint16(m.Perm())
	if m&fs.ModeSetuid != 0 {
		v |= sISUID
	}
	if m&fs.ModeSetgid != 0 {
		v |= sISGID
	}
	if m&fs.ModeSticky != 0 {
		v |= sISVTX
	}
	switch {
	case m&fs.ModeDir != 0:
		v |= sIFDIR
	case m&fs.ModeSymlink != 0:
		v |= sIFLNK
	case m&fs.ModeCharDevice != 0:
		v |= sIFCHR
	case m&fs.ModeDevice != 0:
		v |= sIFBLK
	case m&fs.ModeNamedPipe != 0:
		v |= sIFIFO
	case m&fs.ModeSocket != 0:
		v |= sIFSOCK
	default:
		v |= sIFREG
	}
	return v
}

// modeFromUnix is the inverse of modeToUnix, used by the reader.
func modeFromUnix(v uint16) fs.FileMode {
	m := fs.FileMode(v & 0o777)
	if v&sISUID != 0 {
		m |= fs.ModeSetuid
	}
	if v&sISGID != 0 {
		m |= fs.ModeSetgid
	}
	if v&sISVTX != 0 {
		m |= fs.ModeSticky
	}
	switch v & sIFMT {
	case sIFDIR:
		m |= fs.ModeDir
	case sIFLNK:
		m |= fs.ModeSymlink
	case sIFCHR:
		m |= fs.ModeCharDevice | fs.ModeDevice
	case sIFBLK:
		m |= fs.ModeDevice
	case sIFIFO:
		m |= fs.ModeNamedPipe
	case sIFSOCK:
		m |= fs.ModeSocket
	}
	return m
}

// fileType maps a node mode onto an EROFS_FT_* directory-entry type.
func fileType(m fs.FileMode) uint8 {
	switch {
	case m&fs.ModeDir != 0:
		return ftDir
	case m&fs.ModeSymlink != 0:
		return ftSymlink
	case m&fs.ModeCharDevice != 0:
		return ftChrdev
	case m&fs.ModeDevice != 0:
		return ftBlkdev
	case m&fs.ModeNamedPipe != 0:
		return ftFifo
	case m&fs.ModeSocket != 0:
		return ftSock
	default:
		return ftRegFile
	}
}

// fsforge's tree carries rdev in the classic "old" packing major<<8 | minor,
// matching the OCI/ext engines. EROFS stores the kernel's new_encode_dev form.

func newEncodeDev(rdev uint64) uint32 {
	major := uint32(rdev >> 8)
	minor := uint32(rdev & 0xff)
	return (minor & 0xff) | (major << 8) | ((minor &^ 0xff) << 12)
}

func newDecodeDev(dev uint32) uint64 {
	major := (dev & 0xfff00) >> 8
	minor := (dev & 0xff) | ((dev >> 12) & 0xfff00)
	return uint64(major)<<8 | uint64(minor)
}
