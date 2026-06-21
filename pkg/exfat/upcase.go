package exfat

import "encoding/binary"

// upcaseTableEntries is the number of code units the on-disk up-case table
// covers. The exFAT spec lets the table be shorter than the full 65536 entries:
// any code unit at or beyond DataLength/2 implicitly maps to itself. fsforge
// only folds ASCII a-z, so 128 entries suffice.
//
// Emitting the *full* 65536-entry table is also spec-valid, but it trips a bug
// in exfatprogs <= 1.2.6, whose fsck.exfat misreads a maximum-size table as all
// zeros ("corrupted upcase table 0"). A minimal table is read correctly by
// every exfatprogs version, so we keep it small.
const upcaseTableEntries = 0x80

// buildUpcaseTable returns the exFAT up-case table as a byte slice: little-endian
// UTF-16 entries mapping each covered code unit to its upper-case form. fsforge
// folds ASCII a-z to A-Z, which covers typical filenames; everything else maps
// to itself (explicitly within the table, implicitly beyond it).
func buildUpcaseTable() []byte {
	b := make([]byte, upcaseTableEntries*2)
	for i := 0; i < upcaseTableEntries; i++ {
		u := uint16(i)
		if i >= 'a' && i <= 'z' {
			u = uint16(i - 32)
		}
		binary.LittleEndian.PutUint16(b[i*2:], u)
	}
	return b
}

// upcaseRune folds a code unit using the same rule as the table.
func upcaseRune(r uint16) uint16 {
	if r >= 'a' && r <= 'z' {
		return r - 32
	}
	return r
}

// checksum32 is the exFAT table/boot checksum (rotate-right add) over data.
func checksum32(data []byte, sum uint32) uint32 {
	for _, b := range data {
		sum = (sum >> 1) | (sum << 31)
		sum += uint32(b)
	}
	return sum
}
