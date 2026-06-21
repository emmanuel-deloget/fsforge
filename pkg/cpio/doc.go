// Package cpio implements the cpio "new ASCII" (newc) engine behind the
// image.Filesystem contract — the archive format the Linux kernel unpacks as an
// initramfs.
//
// A cpio archive is a stream, not a block filesystem, but it fits fsforge's
// model the same way squashfs does: "mutation" means rewriting the archive from
// the logical tree. Format streams an uncompressed newc archive (header,
// NUL-terminated name, file body, each 4-byte aligned, ending with the
// TRAILER!!! sentinel and 512-byte padding), and Open parses one back, folding
// hard-linked regular files onto a shared node. The output is a ready-to-boot
// uncompressed initramfs; outer gzip/xz wrapping, when wanted, is applied
// around the archive rather than inside this engine.
package cpio
