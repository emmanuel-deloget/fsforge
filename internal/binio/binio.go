// Package binio holds low-level, module-private helpers shared by engines:
// checksum primitives and binary encoding utilities. It is internal because it
// is an implementation detail, not part of the public surface.
package binio

import "hash/crc32"

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// CRC32C returns the Castagnoli (crc32c) checksum used by ext4 metadata_csum
// and other modern on-disk formats.
func CRC32C(data []byte) uint32 {
	return crc32.Checksum(data, castagnoli)
}

// CRC32CUpdate continues a running crc32c checksum, for checksums computed over
// several non-contiguous regions.
func CRC32CUpdate(crc uint32, data []byte) uint32 {
	return crc32.Update(crc, castagnoli, data)
}
