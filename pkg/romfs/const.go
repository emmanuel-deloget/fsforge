// Package romfs implements the romfs engine behind the image.Filesystem
// contract. romfs is a tiny, uncompressed, big-endian read-only format: a small
// superblock followed by 16-byte-aligned file headers, each a node whose `next`
// field links it to its sibling and whose `spec` field locates a directory's
// first child, a hard link's target or a device's numbers. It stores no
// permissions, owners or timestamps — only a file type and an "executable" bit.
// fsforge writes an image the Linux kernel mounts; the reader parses one back
// into the tree, so romfs doubles as a conversion source.
package romfs

import "encoding/binary"

const (
	// Superblock magic: the words "-rom" and "1fs-" (big-endian).
	magicW0 = 0x2d726f6d
	magicW1 = 0x3166732d

	headerSize = 16          // romfs_inode / superblock fixed part
	align      = 16          // headers and data are 16-byte aligned
	alignMask  = ^uint32(15) // clears the low 4 (flag) bits of an offset

	// File header types (low 3 bits of the `next` field) and the exec flag.
	typeHardlink = 0
	typeDir      = 1
	typeReg      = 2
	typeSymlink  = 3
	typeBlock    = 4
	typeChar     = 5
	typeSocket   = 6
	typeFifo     = 7
	flagExec     = 8

	typeMask = 7
)

var be = binary.BigEndian

// alignUp rounds v up to the next 16-byte boundary.
func alignUp(v uint32) uint32 { return (v + align - 1) &^ (align - 1) }

// paddedName returns name's on-disk length: the name plus a terminating NUL,
// rounded up to a 16-byte boundary.
func paddedName(name string) uint32 { return alignUp(uint32(len(name)) + 1) }
