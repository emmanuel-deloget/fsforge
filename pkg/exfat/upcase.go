package exfat

import "encoding/binary"

// buildUpcaseTable returns the exFAT up-case table as a byte slice: 65536
// little-endian UTF-16 entries mapping each code unit to its upper-case form.
// fsforge folds ASCII a-z to A-Z, which covers typical filenames; everything
// else maps to itself.
func buildUpcaseTable() []byte {
	b := make([]byte, 65536*2)
	for i := 0; i < 65536; i++ {
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
