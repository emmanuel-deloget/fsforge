// Package squashfs implements the squashfs engine behind the image.Filesystem
// contract.
//
// squashfs is a write-once, read-only format, which fits fsforge's model
// cleanly: "mutation" means rebuilding the image from the logical tree rather
// than editing in place. Block compression is supplied through the injected
// compress.Compressor registry (gzip first, then zstd/lz4/xz via pure-Go
// adapters), so this package depends on no specific codec.
package squashfs
