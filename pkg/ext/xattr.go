package ext

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// Extended attributes on ext live in two places: the space left over inside a
// large inode, and — when that runs out — a block of their own referenced by
// i_file_acl. Both hold the same entry format; they differ in what the entry's
// value offset is measured from, which is the detail that makes a reader that
// gets it wrong return somebody else's bytes.
//
// The layout of one area is a header, then entries growing upwards, then values
// growing downwards from the end, meeting in the middle. Entries are sorted by
// (name index, name length, name), which is the order the kernel's lookup
// assumes.

const (
	xattrMagic = 0xEA020000

	// xattrBlockHeaderSize is ext4_xattr_header: magic, refcount, blocks, hash,
	// checksum, then three reserved words.
	xattrBlockHeaderSize = 32
	// xattrIbodyHeaderSize is ext4_xattr_ibody_header: the magic alone.
	xattrIbodyHeaderSize = 4
	// xattrEntrySize is ext4_xattr_entry without its trailing name.
	xattrEntrySize = 16
	// xattrPad is the alignment every entry and value is rounded to.
	xattrPad = 4

	// Hash shifts from fs/ext4/xattr.c.
	nameHashShift  = 5
	valueHashShift = 16
	blockHashShift = 16
)

// xattrPrefixes maps a name index to the prefix it stands for. A name is stored
// as an index plus the remainder, so "security.capability" costs ten bytes on
// disk rather than nineteen. Index 0 means the name is stored whole.
//
// The order matters: the longest match wins, so "system.posix_acl_access" is
// tried before the bare "system." prefix.
var xattrPrefixes = []struct {
	index  uint8
	prefix string
}{
	{2, "system.posix_acl_access"},
	{3, "system.posix_acl_default"},
	{8, "system.richacl"},
	{1, "user."},
	{4, "trusted."},
	{6, "security."},
	{7, "system."},
}

// xattrEntry is one attribute, with its name already split into the index and
// the remainder that goes on disk.
type xattrEntry struct {
	index uint8
	name  string // the part after the prefix
	value []byte
}

// splitXattrName encodes a name as an index and remainder.
func splitXattrName(name string) (uint8, string) {
	for _, p := range xattrPrefixes {
		if p.prefix == name {
			return p.index, "" // whole name is the prefix, e.g. a POSIX ACL
		}
		if strings.HasPrefix(name, p.prefix) && strings.HasSuffix(p.prefix, ".") {
			return p.index, name[len(p.prefix):]
		}
	}
	return 0, name
}

// joinXattrName reverses splitXattrName.
func joinXattrName(index uint8, name string) string {
	for _, p := range xattrPrefixes {
		if p.index == index {
			if !strings.HasSuffix(p.prefix, ".") && name == "" {
				return p.prefix
			}
			return p.prefix + name
		}
	}
	return name
}

// sortedXattrs turns the map into the on-disk order: by index, then name
// length, then name. Sorting here is also what keeps a build reproducible —
// a map range would lay the same attributes out differently each time.
func sortedXattrs(x map[string][]byte) []xattrEntry {
	out := make([]xattrEntry, 0, len(x))
	for name, v := range x {
		idx, rest := splitXattrName(name)
		out = append(out, xattrEntry{index: idx, name: rest, value: v})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.index != b.index {
			return a.index < b.index
		}
		if len(a.name) != len(b.name) {
			return len(a.name) < len(b.name)
		}
		return a.name < b.name
	})
	return out
}

func align4(n int) int { return (n + xattrPad - 1) &^ (xattrPad - 1) }

// entryLen is EXT4_XATTR_LEN: the fixed part plus the name, padded.
func entryLen(nameLen int) int { return align4(xattrEntrySize + nameLen) }

// xattrAreaSize is what the entries and values of x occupy, header excluded,
// including the four-byte terminator that closes the entry list.
func xattrAreaSize(entries []xattrEntry) int {
	n := 4 // terminating zero entry
	for _, e := range entries {
		n += entryLen(len(e.name)) + align4(len(e.value))
	}
	return n
}

// encodeXattrArea writes entries into an area of the given size, with value
// offsets measured from valueBase. The kernel measures those offsets from
// different origins for the two areas — from the start of the block for a block,
// from the first entry for an inode body — so the caller states which.
//
// area must be exactly the space available; values are packed against its end.
func encodeXattrArea(area []byte, entries []xattrEntry, valueBase int) error {
	if got, want := len(area), xattrAreaSize(entries); got < want {
		return fmt.Errorf("ext: extended attributes need %d bytes, %d available", want, got)
	}
	le := binary.LittleEndian
	entOff := 0
	valEnd := len(area) // values are laid down from the end, downwards

	for _, e := range entries {
		vlen := align4(len(e.value))
		valEnd -= vlen
		copy(area[valEnd:], e.value)

		area[entOff] = uint8(len(e.name))
		area[entOff+1] = e.index
		le.PutUint16(area[entOff+2:], uint16(valueBase+valEnd))
		le.PutUint32(area[entOff+4:], 0) // e_value_inum: value is inline
		le.PutUint32(area[entOff+8:], uint32(len(e.value)))
		le.PutUint32(area[entOff+12:], 0) // e_hash, filled by the block encoder
		copy(area[entOff+xattrEntrySize:], e.name)
		entOff += entryLen(len(e.name))
	}
	// The list ends with a zero entry; the area was zero already, but say so.
	le.PutUint32(area[entOff:], 0)
	return nil
}

// decodeXattrArea reads entries back. valueBase mirrors the encoder's, and
// values are looked up in whole, which is the buffer the offsets index into.
func decodeXattrArea(area, whole []byte, valueBase int) map[string][]byte {
	le := binary.LittleEndian
	out := map[string][]byte{}
	for off := 0; off+xattrEntrySize <= len(area); {
		nameLen := int(area[off])
		index := area[off+1]
		if nameLen == 0 && index == 0 {
			break // terminating entry
		}
		if off+xattrEntrySize+nameLen > len(area) {
			break
		}
		valOff := int(le.Uint16(area[off+2:])) - valueBase
		valSize := int(le.Uint32(area[off+8:]))
		name := joinXattrName(index, string(area[off+xattrEntrySize:off+xattrEntrySize+nameLen]))

		// An out-of-range offset means the image is malformed; drop the entry
		// rather than slicing outside the buffer.
		if valOff < 0 || valSize < 0 || valOff+valSize > len(whole) {
			break
		}
		v := make([]byte, valSize)
		copy(v, whole[valOff:valOff+valSize])
		out[name] = v
		off += entryLen(nameLen)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
