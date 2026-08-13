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
	// fast-symlink target / inline bytes / extent tree overlay i_block; written
	// verbatim over the i_block area when non-nil.
	blockRaw []byte
	extra    uint16 // i_extra_isize, for inodes larger than 128 bytes
	fileACL  uint32 // i_file_acl: block holding extended attributes, or 0
	// eaSectors is what the attribute block costs in i_blocks. It is kept apart
	// because the data-block passes assign i_blocks outright, and an increment
	// applied before them would be overwritten.
	eaSectors uint32
	// xattrIbody is the pre-encoded attribute area for the space left over
	// inside a large inode; nil when there is nothing to store there.
	xattrIbody []byte
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
	le.PutUint32(b[104:], n.fileACL) // i_file_acl
	if n.extra > 0 && len(b) >= 130 {
		le.PutUint16(b[128:], n.extra) // i_extra_isize
	}
	if len(n.xattrIbody) > 0 {
		copy(b[int(goodOldInodeSize)+int(n.extra):], n.xattrIbody)
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
	n.fileACL = le.Uint32(b[104:])
	if len(b) >= 130 {
		n.extra = le.Uint16(b[128:])
		// Keep the leftover area verbatim: it is where attributes live when they
		// were small enough to avoid a block of their own.
		if start := goodOldInodeSize + int(n.extra); start < len(b) {
			n.xattrIbody = append([]byte(nil), b[start:]...)
		}
	}
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
