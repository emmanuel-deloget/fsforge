package squashfs

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// Extended attributes on squashfs live in tables of their own, and an inode
// points at them by index rather than holding them. That has one consequence
// that shapes everything here: an inode cannot be written until its index is
// known, so the sets are collected and numbered in a pass before any inode goes
// out, and the tables themselves are written at the end.
//
// Three structures are involved:
//
//   - the key/value table, a metadata stream of (entry, value) pairs;
//   - the id table, one 16-byte record per distinct *set* of attributes, saying
//     where the set's pairs start, how many there are and how long they run;
//   - the index, which says where those two live.
//
// Numbering by set rather than by attribute is what makes the format cheap on a
// real rootfs: ten thousand files sharing one SELinux label share one record.
// Deduplication here is not an optimisation bolted on, it is how the format is
// meant to be used.

const (
	// Attribute name prefixes, encoded as the entry's type.
	xattrTypeUser     = 0
	xattrTypeTrusted  = 1
	xattrTypeSecurity = 2

	// xattrIDNone is the inode's "no attributes" marker.
	xattrIDNone = 0xFFFFFFFF

	// xattrIDSize is one squashfs_xattr_id: a reference, a count and a size.
	xattrIDSize = 16
	// xattrIndexSize is squashfs_xattr_id_table: table start, id count, unused.
	xattrIndexSize = 16

	xattrIDsPerMetaBlock = metaBlockSize / xattrIDSize
)

var xattrPrefixes = []struct {
	typ    uint16
	prefix string
}{
	{xattrTypeUser, "user."},
	{xattrTypeTrusted, "trusted."},
	{xattrTypeSecurity, "security."},
}

// splitXattrName encodes a name as a type and the remainder. squashfs has no
// escape for a name outside the three prefixes, so those are refused rather
// than written as something they are not.
func splitXattrName(name string) (uint16, string, error) {
	for _, p := range xattrPrefixes {
		if strings.HasPrefix(name, p.prefix) {
			return p.typ, name[len(p.prefix):], nil
		}
	}
	return 0, "", fmt.Errorf("squashfs: attribute %q has no prefix the format can encode "+
		"(user., trusted. or security.)", name)
}

func joinXattrName(typ uint16, rest string) string {
	for _, p := range xattrPrefixes {
		if p.typ == typ&0xFF {
			return p.prefix + rest
		}
	}
	return rest
}

// xattrSet is one node's attributes, flattened and ordered so that two nodes
// carrying the same attributes produce the same bytes and share one record.
type xattrSet struct {
	pairs []xattrPair
	key   string // the serialised form, used for deduplication
}

type xattrPair struct {
	typ   uint16
	name  string
	value []byte
}

func newXattrSet(x map[string][]byte) (xattrSet, error) {
	var s xattrSet
	for name, v := range x {
		typ, rest, err := splitXattrName(name)
		if err != nil {
			return s, err
		}
		s.pairs = append(s.pairs, xattrPair{typ: typ, name: rest, value: v})
	}
	sort.Slice(s.pairs, func(i, j int) bool {
		a, b := s.pairs[i], s.pairs[j]
		if a.typ != b.typ {
			return a.typ < b.typ
		}
		return a.name < b.name
	})
	var sb strings.Builder
	for _, p := range s.pairs {
		fmt.Fprintf(&sb, "%d\x00%s\x00%s\x00", p.typ, p.name, p.value)
	}
	s.key = sb.String()
	return s, nil
}

// encode renders the set's key/value pairs as they appear in the table.
func (s xattrSet) encode() []byte {
	var out []byte
	for _, p := range s.pairs {
		var hdr [4]byte
		le.PutUint16(hdr[0:], p.typ)
		le.PutUint16(hdr[2:], uint16(len(p.name)))
		out = append(out, hdr[:]...)
		out = append(out, p.name...)

		var vh [4]byte
		le.PutUint32(vh[0:], uint32(len(p.value)))
		out = append(out, vh[:]...)
		out = append(out, p.value...)
	}
	return out
}

// listSize is the id record's size field. It is not the encoded length, which
// is what it looks like: mksquashfs stores what listxattr and getxattr together
// would return — for each attribute the full prefixed name, its terminator and
// its value. Writing the encoded length instead produces a number a reader
// bounding its work by it will get wrong.
func (s xattrSet) listSize() uint32 {
	var n uint32
	for _, p := range s.pairs {
		n += uint32(len(joinXattrName(p.typ, p.name)) + 1 + len(p.value))
	}
	return n
}

// collectXattrs walks the tree and numbers every distinct attribute set, so an
// inode can be written with an index into a table that does not exist yet.
func (w *swriter) collectXattrs(n *image.Node, seen map[*image.Node]bool) error {
	if seen[n] {
		return nil
	}
	seen[n] = true

	if len(n.Xattrs) > 0 {
		set, err := newXattrSet(n.Xattrs)
		if err != nil {
			return err
		}
		id, ok := w.xattrIDs[set.key]
		if !ok {
			id = uint32(len(w.xattrSets))
			w.xattrIDs[set.key] = id
			w.xattrSets = append(w.xattrSets, set)
		}
		w.xattrOf[n] = id
	}
	if n.IsDir() {
		for _, e := range sortChildren(n) {
			if err := w.collectXattrs(e.Node, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// xattrIndexOf is the value an inode stores: the set's number, or the marker
// that says there are none.
func (w *swriter) xattrIndexOf(n *image.Node) uint32 {
	if id, ok := w.xattrOf[n]; ok {
		return id
	}
	return xattrIDNone
}

// superFlags reports the superblock flags. The no-xattrs flag is a promise to
// the reader that it can skip the tables entirely, so it must come off exactly
// when there is something to find.
func (w *swriter) superFlags() uint16 {
	f := uint16(flagNoFragments)
	if len(w.xattrSets) == 0 {
		f |= flagNoXattrs
	}
	return f
}

// writeXattrTables emits the key/value stream, the id records and the index,
// and returns where the index starts. It returns noTable when the image has no
// attributes at all, which is what the superblock field expects.
func (w *swriter) writeXattrTables() (uint64, uint32) {
	if len(w.xattrSets) == 0 {
		return noTable, 0
	}

	kv := &metaWriter{comp: w.comp}
	ids := make([]byte, 0, len(w.xattrSets)*xattrIDSize)
	for _, set := range w.xattrSets {
		block, offset := kv.ref()
		body := set.encode()
		kv.write(body)

		var rec [xattrIDSize]byte
		le.PutUint64(rec[0:], uint64(block)<<16|uint64(offset))
		le.PutUint32(rec[8:], uint32(len(set.pairs)))
		le.PutUint32(rec[12:], set.listSize())
		ids = append(ids, rec[:]...)
	}
	kv.finish()

	kvStart := uint64(w.pos)
	w.writeAt(kv.out)

	// The id records go out as their own metadata stream, and the index records
	// where each of its blocks landed.
	var idBlockOffsets []uint64
	for i := 0; i < len(ids); i += xattrIDsPerMetaBlock * xattrIDSize {
		end := min(i+xattrIDsPerMetaBlock*xattrIDSize, len(ids))
		idBlockOffsets = append(idBlockOffsets, uint64(w.pos))
		w.writeAt(metaBlock(w.comp, ids[i:end]))
	}

	indexStart := uint64(w.pos)
	idx := make([]byte, xattrIndexSize+8*len(idBlockOffsets))
	le.PutUint64(idx[0:], kvStart)
	le.PutUint32(idx[8:], uint32(len(w.xattrSets)))
	// idx[12:16] is unused.
	for k, off := range idBlockOffsets {
		le.PutUint64(idx[xattrIndexSize+k*8:], off)
	}
	w.writeAt(idx)
	return indexStart, uint32(len(w.xattrSets))
}

// --- reading ---

// readXattrTables loads the attribute tables an image carries, leaving the
// reader with one decoded set per id so an inode's index resolves without
// further seeking.
func (r *squashReader) readXattrTables() error {
	start := int64(r.sb.xattrTableStart)
	if start < 0 || uint64(r.sb.xattrTableStart) == noTable {
		return nil // no attributes in this image
	}
	idx := make([]byte, xattrIndexSize)
	if _, err := r.dev.ReadAt(idx, start); err != nil && err != io.EOF {
		return err
	}
	kvStart := int64(le.Uint64(idx[0:]))
	count := int(le.Uint32(idx[8:]))
	if count == 0 {
		return nil
	}

	// The key/value stream runs from its start up to the id blocks, which begin
	// straight after it and end at the index.
	blocks := (count + xattrIDsPerMetaBlock - 1) / xattrIDsPerMetaBlock
	offsets := make([]byte, 8*blocks)
	if _, err := r.dev.ReadAt(offsets, start+xattrIndexSize); err != nil && err != io.EOF {
		return err
	}
	firstIDBlock := int64(le.Uint64(offsets[0:]))

	kv, kvMap, err := r.readMetaTable(kvStart, firstIDBlock)
	if err != nil {
		return err
	}
	ids, _, err := r.readMetaTable(firstIDBlock, start)
	if err != nil {
		return err
	}

	r.xattrs = make([]map[string][]byte, 0, count)
	for i := 0; i < count; i++ {
		rec := ids[i*xattrIDSize:]
		if len(rec) < xattrIDSize {
			break
		}
		ref := le.Uint64(rec[0:])
		block := uint32(ref >> 16)
		offset := int(ref & 0xFFFF)
		// The record's size field counts prefixed names and values, not encoded
		// bytes, so the count is what says when to stop reading pairs.
		count := int(le.Uint32(rec[8:]))

		base, ok := kvMap[block]
		if !ok || base+offset > len(kv) {
			r.xattrs = append(r.xattrs, nil)
			continue
		}
		r.xattrs = append(r.xattrs, decodeXattrPairs(kv[base+offset:], count))
	}
	return nil
}

// decodeXattrPairs reads count (entry, value) pairs from the start of b.
func decodeXattrPairs(b []byte, count int) map[string][]byte {
	out := map[string][]byte{}
	for off := 0; count > 0 && off+4 <= len(b); count-- {
		typ := le.Uint16(b[off:])
		nameLen := int(le.Uint16(b[off+2:]))
		off += 4
		if off+nameLen+4 > len(b) {
			break
		}
		name := string(b[off : off+nameLen])
		off += nameLen

		vsize := int(le.Uint32(b[off:]))
		off += 4
		if off+vsize > len(b) {
			break
		}
		v := make([]byte, vsize)
		copy(v, b[off:off+vsize])
		off += vsize
		out[joinXattrName(typ, name)] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// xattrsAt resolves an inode's stored index.
func (r *squashReader) xattrsAt(id uint32) map[string][]byte {
	if id == xattrIDNone || int(id) >= len(r.xattrs) {
		return nil
	}
	return r.xattrs[id]
}
