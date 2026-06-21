// Package erofs implements the EROFS (Enhanced Read-Only File System) engine
// behind the image.Filesystem contract.
//
// EROFS is a write-once, read-only format, which fits fsforge's model cleanly:
// "mutation" means rebuilding the image from the logical tree rather than
// editing in place. The writer emits uncompressed images — 4 KiB blocks,
// 64-byte extended inodes and FLAT_PLAIN data — that the Linux kernel and
// fsck.erofs accept. The reader additionally understands compact inodes and
// inline tails, so a default mkfs.erofs image can be opened as a conversion
// source; compressed images are rejected because fsforge ships no EROFS
// decompressor.
package erofs
