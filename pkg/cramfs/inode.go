package cramfs

import "io/fs"

// cinode is a decoded cramfs_inode. The on-disk form packs the fields into three
// little-endian words via bitfields: mode:16|uid:16, size:24|gid:8 and
// namelen:6|offset:26. namelen is the name length in 4-byte units and offset is
// the data location in 4-byte units; size doubles as the device number for
// device nodes.
type cinode struct {
	mode    uint32
	uid     uint32
	size    uint32
	gid     uint32
	namelen uint32 // name length / 4
	offset  uint32 // data offset / 4
}

func (in cinode) marshal() []byte {
	b := make([]byte, inodeSize)
	le.PutUint32(b[0:], in.mode&0xffff|(in.uid&0xffff)<<16)
	le.PutUint32(b[4:], in.size&0xffffff|(in.gid&0xff)<<24)
	le.PutUint32(b[8:], in.namelen&0x3f|(in.offset&0x3ffffff)<<6)
	return b
}

func parseInode(b []byte) cinode {
	w0 := le.Uint32(b[0:])
	w1 := le.Uint32(b[4:])
	w2 := le.Uint32(b[8:])
	return cinode{
		mode:    w0 & 0xffff,
		uid:     w0 >> 16,
		size:    w1 & 0xffffff,
		gid:     w1 >> 24,
		namelen: w2 & 0x3f,
		offset:  w2 >> 6,
	}
}

// modeToUnix turns a Go file mode into a 16-bit Unix st_mode.
func modeToUnix(m fs.FileMode) uint32 {
	v := uint32(m.Perm())
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

// modeFromUnix is the inverse of modeToUnix.
func modeFromUnix(v uint32) fs.FileMode {
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
