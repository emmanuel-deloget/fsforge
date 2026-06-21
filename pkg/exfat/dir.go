package exfat

import (
	"time"
	"unicode/utf16"
)

// Entry type bytes.
const (
	entVolumeLabel = 0x83
	entBitmap      = 0x81
	entUpcase      = 0x82
	entFile        = 0x85
	entStream      = 0xC0
	entFileName    = 0xC1

	attrReadOnly  = 0x01
	attrDirectory = 0x10
	attrArchive   = 0x20

	flagAllocPossible = 0x01
	flagNoFatChain    = 0x02
)

// nameUTF16 returns the UTF-16 code units of a name.
func nameUTF16(name string) []uint16 { return utf16.Encode([]rune(name)) }

// nameHash computes the exFAT name hash over the up-cased UTF-16LE name.
func nameHash(units []uint16) uint16 {
	var h uint16
	for _, u := range units {
		up := upcaseRune(u)
		for _, b := range []byte{byte(up), byte(up >> 8)} {
			h = (h >> 1) | (h << 15)
			h += uint16(b)
		}
	}
	return h
}

// setChecksum computes the entry-set checksum over the concatenated 32-byte
// entries, skipping the checksum field (bytes 2-3 of the first entry).
func setChecksum(entries []byte) uint16 {
	var sum uint16
	for i, b := range entries {
		if i == 2 || i == 3 {
			continue
		}
		sum = (sum >> 1) | (sum << 15)
		sum += uint16(b)
	}
	return sum
}

// timestamp encodes a time into the exFAT 4-byte timestamp plus the 10ms and
// UTC-offset bytes.
func timestamp(t time.Time) (packed uint32, tenms byte, utc byte) {
	t = t.UTC()
	y := t.Year()
	if y < 1980 {
		y = 1980
	}
	packed = uint32(t.Second()/2) |
		uint32(t.Minute())<<5 |
		uint32(t.Hour())<<11 |
		uint32(t.Day())<<16 |
		uint32(t.Month())<<21 |
		uint32(y-1980)<<25
	tenms = byte((t.Second() % 2) * 100)
	utc = 0 // UTC
	return
}

// fileAttr maps a node to exFAT file attributes.
func fileAttr(isDir bool) uint16 {
	if isDir {
		return attrDirectory
	}
	return attrArchive
}

// nameEntries returns the count of File Name entries a name needs (15 units each).
func nameEntries(name string) int {
	n := len(nameUTF16(name))
	return (n + 14) / 15
}
