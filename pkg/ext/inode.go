package ext

import (
	"encoding/binary"
	"io/fs"
)

// inode is the in-memory form of the 128-byte on-disk inode fields fsforge uses.
type inode struct {
	mode       uint16
	uid        uint16
	size       uint32
	atime      uint32
	ctime      uint32
	mtime      uint32
	dtime      uint32
	gid        uint16
	linksCount uint16
	blocks     uint32 // in 512-byte units
	flags      uint32
	block      [totalIBlocks]uint32
	// fast-symlink target / inline bytes overlay i_block; written via blockRaw.
	blockRaw []byte // when non-nil, written verbatim over the i_block area
}

func (n *inode) marshalInto(b []byte) {
	le := binary.LittleEndian
	le.PutUint16(b[0:], n.mode)
	le.PutUint16(b[2:], n.uid)
	le.PutUint32(b[4:], n.size)
	le.PutUint32(b[8:], n.atime)
	le.PutUint32(b[12:], n.ctime)
	le.PutUint32(b[16:], n.mtime)
	le.PutUint32(b[20:], n.dtime)
	le.PutUint16(b[24:], n.gid)
	le.PutUint16(b[26:], n.linksCount)
	le.PutUint32(b[28:], n.blocks)
	le.PutUint32(b[32:], n.flags)
	if n.blockRaw != nil {
		copy(b[40:40+fastSymlinkMax], n.blockRaw)
	} else {
		for i := 0; i < totalIBlocks; i++ {
			le.PutUint32(b[40+i*4:], n.block[i])
		}
	}
}

func parseInode(b []byte) inode {
	le := binary.LittleEndian
	var n inode
	n.mode = le.Uint16(b[0:])
	n.uid = le.Uint16(b[2:])
	n.size = le.Uint32(b[4:])
	n.atime = le.Uint32(b[8:])
	n.ctime = le.Uint32(b[12:])
	n.mtime = le.Uint32(b[16:])
	n.dtime = le.Uint32(b[20:])
	n.gid = le.Uint16(b[24:])
	n.linksCount = le.Uint16(b[26:])
	n.blocks = le.Uint32(b[28:])
	n.flags = le.Uint32(b[32:])
	for i := 0; i < totalIBlocks; i++ {
		n.block[i] = le.Uint32(b[40+i*4:])
	}
	n.blockRaw = append([]byte(nil), b[40:40+totalIBlocks*4]...)
	return n
}

// extMode maps a Go file mode to the ext2 i_mode value.
func extMode(m fs.FileMode) uint16 {
	v := uint16(m.Perm())
	if m&fs.ModeSetuid != 0 {
		v |= modeSetuid
	}
	if m&fs.ModeSetgid != 0 {
		v |= modeSetgid
	}
	if m&fs.ModeSticky != 0 {
		v |= modeSticky
	}
	switch {
	case m&fs.ModeDir != 0:
		v |= modeDir
	case m&fs.ModeSymlink != 0:
		v |= modeSymlink
	case m&fs.ModeCharDevice != 0:
		v |= modeChrdev
	case m&fs.ModeDevice != 0:
		v |= modeBlkdev
	case m&fs.ModeNamedPipe != 0:
		v |= modeFifo
	case m&fs.ModeSocket != 0:
		v |= modeSock
	default:
		v |= modeRegFile
	}
	return v
}

// dirFileType maps a Go file mode to the directory-entry file_type byte.
func dirFileType(m fs.FileMode) byte {
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
