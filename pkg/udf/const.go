// Package udf implements the UDF (Universal Disk Format) engine behind the
// image.Filesystem contract: ECMA-167 volume/file structures constrained by the
// OSTA UDF specification (revision 2.01). fsforge writes a read-only UDF image
// (2048-byte blocks, a single Type-1 read-only partition, File Entries with
// short allocation descriptors) that the Linux kernel mounts and udfinfo reads;
// the reader parses such an image back into the tree, so UDF doubles as a
// conversion source.
package udf

import "encoding/binary"

const (
	blockSize   = 2048
	descVersion = 3 // ECMA-167 3rd edition descriptors (UDF 2.01)
	udfRevision = 0x0201

	// Fixed image prefix, in 2048-byte blocks.
	vrsBlock  = 16  // Volume Recognition Sequence (BEA01/NSR03/TEA01)
	mvdsBlock = 20  // Main Volume Descriptor Sequence
	mvdsLen   = 16  // blocks reserved for a VDS
	rvdsBlock = 36  // Reserve Volume Descriptor Sequence
	lvidBlock = 52  // Logical Volume Integrity Descriptor
	avdpBlock = 256 // first Anchor Volume Descriptor Pointer
	partBlock = 257 // partition starting location (absolute)
)

// Descriptor tag identifiers (ECMA-167 3/7.2.1 and 4/7.2.1).
const (
	tagPVD  = 0x0001
	tagAVDP = 0x0002
	tagIUVD = 0x0004
	tagPD   = 0x0005
	tagLVD  = 0x0006
	tagUSD  = 0x0007
	tagTD   = 0x0008
	tagLVID = 0x0009
	tagFSD  = 0x0100
	tagFID  = 0x0101
	tagFE   = 0x0105
	tagEFE  = 0x010A
)

// ICB file types (ECMA-167 4/14.6.6).
const (
	ftDirectory = 0x04
	ftRegular   = 0x05
	ftBlock     = 0x06
	ftChar      = 0x07
	ftFIFO      = 0x09
	ftSocket    = 0x0A
	ftSymlink   = 0x0C
)

// ICB flags (ECMA-167 4/14.6.8): allocation-descriptor kind plus mode bits.
const (
	adShort   = 0x0000
	adInICB   = 0x0003
	icbSetuid = 0x0040
	icbSetgid = 0x0080
	icbSticky = 0x0100
)

// File characteristics in a File Identifier Descriptor (ECMA-167 4/14.4.3).
const (
	fidDirectory = 0x02
	fidParent    = 0x08
)

// Partition access types (ECMA-167 3/10.5.7).
const pdAccessReadOnly = 0x00000001

// Extent length high bits encode the extent kind (ECMA-167 4/14.14.1.1); a
// recorded-and-allocated extent uses type 0, so the length stands alone.
const extLenTypeMask = 0xC0000000

// File Entry permission bits (ECMA-167 4/14.9.5).
const (
	permOExec  = 0x00000001
	permOWrite = 0x00000002
	permORead  = 0x00000004
	permGExec  = 0x00000020
	permGWrite = 0x00000040
	permGRead  = 0x00000080
	permUExec  = 0x00000400
	permUWrite = 0x00000800
	permURead  = 0x00001000
)

var le = binary.LittleEndian
