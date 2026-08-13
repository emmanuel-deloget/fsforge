package squashfs

// On-disk constants for squashfs 4.0 (see kernel Documentation/filesystems/
// squashfs.txt and the squashfs-tools sources).
const (
	magic        = 0x73717368 // "hsqs"
	versionMajor = 4
	versionMinor = 0

	superblockSize = 96
	metaBlockSize  = 8192 // max uncompressed metadata block

	defaultBlockSize = 131072 // 128 KiB data blocks

	// Inode types. The extended variants exist to carry an extended-attribute
	// index; fsforge emits one only for a node that has attributes, so an image
	// without them is byte-identical to what it was before.
	typeDir     = 1
	typeFile    = 2
	typeSymlink = 3
	typeBlkdev  = 4
	typeChrdev  = 5
	typeFifo    = 6
	typeSocket  = 7

	typeExtDir     = 8
	typeExtFile    = 9
	typeExtSymlink = 10
	typeExtBlkdev  = 11
	typeExtChrdev  = 12
	typeExtFifo    = 13
	typeExtSocket  = 14

	// Superblock flags.
	flagNoFragments = 0x0010
	flagNoXattrs    = 0x0200

	// Marker bits.
	metaUncompressed  = 0x8000     // metadata block header: stored uncompressed
	blockUncompressed = 0x01000000 // data block size field: stored uncompressed

	noFragment = 0xFFFFFFFF
	noTable    = 0xFFFFFFFFFFFFFFFF // -1 for absent tables (xattr, fragment, export)

	idsPerMetaBlock = metaBlockSize / 4
)
