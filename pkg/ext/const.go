package ext

// On-disk constants for the ext2/3/4 family. Values follow the kernel's
// Documentation/filesystems/ext4 and the historical ext2 layout.
const (
	superblockOffset = 1024 // primary superblock byte offset
	superblockSize   = 1024
	magic            = 0xEF53

	rootIno      = 2  // root directory inode
	firstIno     = 11 // first non-reserved inode (also lost+found)
	lostFoundIno = 11
	reservedInos = 10 // inodes 1..10 are reserved

	goodOldInodeSize = 128
	ext4InodeSize    = 256
	extraISize       = 32 // i_extra_isize for inodes larger than 128 bytes
	descSize         = 32 // ext2 group descriptor size

	defaultBlockSize     = 1024
	ext4DefaultBlockSize = 4096
	bytesPerInode        = 16384 // sizing heuristic, as in mke2fs

	// Revision levels.
	dynamicRev = 1

	// Superblock state / errors.
	stateClean     = 1
	errorsContinue = 1
	creatorLinux   = 0

	// Feature flags we set.
	featIncompatFiletype    = 0x0002
	featIncompatExtents     = 0x0040
	featRoCompatSparseSuper = 0x0001

	// Inode flags.
	extentsFL = 0x80000 // inode uses extents (ext4)

	// Extent tree.
	extentMagic  = 0xF30A
	extentMaxLen = 32768 // max blocks in one initialised extent

	// Directory entry file types (EXT2_FEATURE_INCOMPAT_FILETYPE).
	ftUnknown = 0
	ftRegFile = 1
	ftDir     = 2
	ftChrdev  = 3
	ftBlkdev  = 4
	ftFifo    = 5
	ftSock    = 6
	ftSymlink = 7

	// ext2 i_mode type bits.
	modeFifo    = 0x1000
	modeChrdev  = 0x2000
	modeDir     = 0x4000
	modeBlkdev  = 0x6000
	modeRegFile = 0x8000
	modeSymlink = 0xA000
	modeSock    = 0xC000

	modeSetuid = 0x800
	modeSetgid = 0x400
	modeSticky = 0x200

	directBlocks   = 12 // i_block[0..11]
	indSingle      = 12 // i_block[12]
	indDouble      = 13 // i_block[13]
	indTriple      = 14 // i_block[14]
	totalIBlocks   = 15
	fastSymlinkMax = 60 // bytes that fit in the i_block area
)
