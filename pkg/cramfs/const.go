// Package cramfs implements the cramfs (Compressed ROM File System) engine
// behind the image.Filesystem contract. cramfs is a small, write-once, read-only
// format: file data is split into 4 KiB blocks, each independently zlib
// compressed, and inodes are stored inline in their parent directory's entries.
// fsforge writes a little-endian image the Linux kernel mounts; the reader
// parses one back into the tree, so cramfs doubles as a conversion source.
package cramfs

import "encoding/binary"

const (
	magic     = 0x28cd3d45
	signature = "Compressed ROMFS"

	superblockSize  = 76 // magic..root inode
	rootInodeOffset = 64 // root cramfs_inode within the superblock
	crcOffset       = 32 // fsid.crc
	inodeSize       = 12
	blockSize       = 4096

	// Feature flags (cramfs_fs.h). FSID v2 carries size/blocks/files; dirs are
	// sorted; the root offset is stored shifted (>>2) like a real offset.
	flagFSIDv2      = 0x0001
	flagSortedDirs  = 0x0002
	flagShiftedRoot = 0x0400

	// Block pointer flag: the block is stored uncompressed.
	blkUncompressed = 1 << 31
)

// Unix st_mode constants (cramfs stores the full 16-bit st_mode).
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

var le = binary.LittleEndian
