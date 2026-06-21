package erofs

import "encoding/binary"

// On-disk constants for EROFS (Enhanced Read-Only File System), the v1 128-byte
// superblock layout. See the Linux kernel's fs/erofs/erofs_fs.h. fsforge writes
// uncompressed images: 64-byte extended inodes whose data uses the FLAT_PLAIN
// layout (no inline tail, no compression), which every kernel that supports
// EROFS accepts.
const (
	superMagic  = 0xE0F5E1E2 // EROFS_SUPER_MAGIC_V1
	superOffset = 1024       // EROFS_SUPER_OFFSET
	superSize   = 128        // on-disk superblock size (sb_extslots == 0)

	blkSizeBits = 12               // log2(blockSize); EROFS historically == PAGE_SIZE
	blockSize   = 1 << blkSizeBits // 4096-byte filesystem blocks
	metaBlkAddr = 0                // metadata (inode) area starts at block 0

	inodeCompactSize  = 32 // erofs_inode_compact
	inodeExtendedSize = 64 // erofs_inode_extended
	nidSlot           = 32 // nid addressing unit (bytes)

	direntSize = 12 // erofs_dirent: nid(8) nameoff(2) file_type(1) reserved(1)

	// i_format: bit 0 is the version (0 compact, 1 extended); bits 1..3 select
	// the data layout. fsforge writes extended inodes with the FLAT_PLAIN layout.
	inodeVersionCompact  = 0
	inodeVersionExtended = 1
	datalayoutFlatPlain  = 0 // FLAT_PLAIN:  data in raw blocks at i_u.raw_blkaddr
	datalayoutFlatInline = 2 // FLAT_INLINE: tail block inline after the inode

	formatExtendedFlatPlain = inodeVersionExtended | (datalayoutFlatPlain << 1) // 1

	// EROFS_FT_* directory-entry file types (same numbering as the kernel's
	// generic FT_* set).
	ftUnknown = 0
	ftRegFile = 1
	ftDir     = 2
	ftChrdev  = 3
	ftBlkdev  = 4
	ftFifo    = 5
	ftSock    = 6
	ftSymlink = 7
)

var le = binary.LittleEndian
