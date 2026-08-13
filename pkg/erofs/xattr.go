package erofs

import (
	"fmt"
	"sort"
	"strings"
)

// Extended attributes on EROFS sit immediately after the inode they belong to,
// in the same inode area: a small header, then one entry per attribute. The
// inode's i_xattr_icount says how much of that space is in use, counted in
// four-byte units past the header — which is why an inode with attributes takes
// more than its 64 bytes and the nid layout has to account for it.
//
// fsforge writes only inline attributes. EROFS can also share one attribute
// across inodes out of a separate table, which saves space on a rootfs where
// thousands of files carry the same SELinux label; that is an optimisation, not
// a capability, and the images are correct without it.

const (
	// xattrHeaderSize is erofs_xattr_ibody_header: a name filter, a shared
	// count, then reserved bytes.
	xattrHeaderSize = 12
	// xattrEntrySize is erofs_xattr_entry without its name or value.
	xattrEntrySize = 4
	// xattrAlign is what the header and every entry are rounded to.
	xattrAlign = 4
)

// xattrPrefixes maps a name index to the prefix it stands for, longest first so
// "system.posix_acl_access" is matched before any shorter prefix.
var xattrPrefixes = []struct {
	index  uint8
	prefix string
}{
	{2, "system.posix_acl_access"},
	{3, "system.posix_acl_default"},
	{1, "user."},
	{4, "trusted."},
	{5, "lustre."},
	{6, "security."},
}

type xattrEntry struct {
	index uint8
	name  string // the part after the prefix
	value []byte
}

func splitXattrName(name string) (uint8, string) {
	for _, p := range xattrPrefixes {
		if p.prefix == name {
			return p.index, ""
		}
		if strings.HasSuffix(p.prefix, ".") && strings.HasPrefix(name, p.prefix) {
			return p.index, name[len(p.prefix):]
		}
	}
	return 0, name
}

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

// sortedXattrs puts the attributes in a fixed order. Nothing on disk requires
// one, but ranging the map would lay the same attributes out differently on
// every build, and reproducibility is the whole point.
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
		return a.name < b.name
	})
	return out
}

func alignUp(n, a int) int { return (n + a - 1) &^ (a - 1) }

// xattrEntryLen is EROFS_XATTR_ENTRY_SIZE: header, name and value, padded.
func xattrEntryLen(e xattrEntry) int {
	return alignUp(xattrEntrySize+len(e.name)+len(e.value), xattrAlign)
}

// xattrInlineSize is the whole inline area for x, header included, or zero when
// there is nothing to store.
func xattrInlineSize(entries []xattrEntry) int {
	if len(entries) == 0 {
		return 0
	}
	n := xattrHeaderSize
	for _, e := range entries {
		n += xattrEntryLen(e)
	}
	return n
}

// xattrICount converts an inline area size to the i_xattr_icount the inode
// carries: the count is in four-byte units past the header, biased by one, so
// that zero can mean "no attributes at all".
func xattrICount(size int) uint16 {
	if size == 0 {
		return 0
	}
	return uint16((size-xattrHeaderSize)/xattrAlign + 1)
}

// encodeXattrs renders the inline area. Values follow their name inside the
// entry, so an entry is self-contained and there is no offset to get wrong.
func encodeXattrs(entries []xattrEntry) ([]byte, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	buf := make([]byte, xattrInlineSize(entries))
	// h_name_filter stays zero: every bit set to one would claim the
	// corresponding name is absent, and zero means "look and see".
	// h_shared_count is zero because nothing is shared out of a table.
	off := xattrHeaderSize
	for _, e := range entries {
		if len(e.name) > 0xFF {
			return nil, fmt.Errorf("erofs: attribute name too long (%d bytes): %q", len(e.name), e.name)
		}
		if len(e.value) > 0xFFFF {
			return nil, fmt.Errorf("erofs: attribute value too long (%d bytes) for %q", len(e.value), e.name)
		}
		buf[off] = uint8(len(e.name))
		buf[off+1] = e.index
		le.PutUint16(buf[off+2:], uint16(len(e.value)))
		copy(buf[off+xattrEntrySize:], e.name)
		copy(buf[off+xattrEntrySize+len(e.name):], e.value)
		off += xattrEntryLen(e)
	}
	return buf, nil
}

// decodeXattrs reads an inline area back.
func decodeXattrs(area []byte) map[string][]byte {
	if len(area) <= xattrHeaderSize {
		return nil
	}
	out := map[string][]byte{}
	for off := xattrHeaderSize; off+xattrEntrySize <= len(area); {
		nameLen := int(area[off])
		index := area[off+1]
		valSize := int(le.Uint16(area[off+2:]))
		end := off + xattrEntrySize + nameLen + valSize
		if nameLen == 0 && valSize == 0 && index == 0 {
			break // padding, not an entry
		}
		if end > len(area) {
			break
		}
		name := joinXattrName(index, string(area[off+xattrEntrySize:off+xattrEntrySize+nameLen]))
		v := make([]byte, valSize)
		copy(v, area[off+xattrEntrySize+nameLen:end])
		out[name] = v
		off += alignUp(xattrEntrySize+nameLen+valSize, xattrAlign)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
