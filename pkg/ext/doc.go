// Package ext implements the ext2/ext3/ext4 family of engines behind the
// image.Filesystem contract.
//
// The three share a single code path: ext2 is the base layout (superblock,
// group descriptors, block/inode bitmaps, inode table, linked directories);
// ext3 adds an empty, freshly initialised journal; ext4 adds extents, 64-bit
// fields, metadata checksums, htree directories and flex_bg on top. Because
// fsforge only ever operates offline, journal recovery is never implemented:
// on Open an existing journal is replayed to obtain a consistent state, and on
// Finalize a fresh empty journal is written.
//
// On-disk structure encoding/decoding, the half_md4 htree hash and crc32c
// checksums are format-mandated and live unexported in this package with their
// own golden-vector tests; they are deliberately not injected.
package ext
