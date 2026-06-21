package udf

import (
	"io/fs"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// feHeaderLen is the fixed part of a File Entry, before extended attributes and
// allocation descriptors (ECMA-167 4/14.9).
const feHeaderLen = 176

// fileTypeOf maps a node mode onto an ICB file type.
func fileTypeOf(m fs.FileMode) uint8 {
	switch {
	case m&fs.ModeDir != 0:
		return ftDirectory
	case m&fs.ModeSymlink != 0:
		return ftSymlink
	case m&fs.ModeCharDevice != 0:
		return ftChar
	case m&fs.ModeDevice != 0:
		return ftBlock
	case m&fs.ModeNamedPipe != 0:
		return ftFIFO
	case m&fs.ModeSocket != 0:
		return ftSocket
	default:
		return ftRegular
	}
}

// udfPerms maps a Go permission set onto UDF File Entry permission bits.
func udfPerms(m fs.FileMode) uint32 {
	p := m.Perm()
	var v uint32
	for _, b := range []struct {
		mask fs.FileMode
		bit  uint32
	}{
		{0o400, permURead}, {0o200, permUWrite}, {0o100, permUExec},
		{0o040, permGRead}, {0o020, permGWrite}, {0o010, permGExec},
		{0o004, permORead}, {0o002, permOWrite}, {0o001, permOExec},
	} {
		if p&b.mask != 0 {
			v |= b.bit
		}
	}
	return v
}

// icbModeFlags returns the setuid/setgid/sticky ICB flags for a mode.
func icbModeFlags(m fs.FileMode) uint16 {
	var f uint16
	if m&fs.ModeSetuid != 0 {
		f |= icbSetuid
	}
	if m&fs.ModeSetgid != 0 {
		f |= icbSetgid
	}
	if m&fs.ModeSticky != 0 {
		f |= icbSticky
	}
	return f
}

// buildFE renders a File Entry for n. dataLen is the information length,
// dataBlocks the number of recorded blocks, ads the encoded allocation
// descriptors (already short_ad-formatted) and ea the extended-attribute area
// (for device nodes); both may be empty.
func (w *uwriter) buildFE(n *image.Node, lbn uint32, dataLen uint64, dataBlocks uint64, ads, ea []byte) []byte {
	b := make([]byte, feHeaderLen+len(ea)+len(ads))

	// ICB tag (offset 16, 20 bytes).
	icb := b[16:]
	le.PutUint16(icb[4:], 4) // strategy type 4
	le.PutUint16(icb[8:], 1) // numEntries
	icb[11] = fileTypeOf(n.Mode)
	if p := w.parent[n]; p != nil {
		le.PutUint32(icb[12:], w.feLbn[p]) // parentICBLocation lbn
	}
	le.PutUint16(icb[18:], adShort|icbModeFlags(n.Mode))

	le.PutUint32(b[36:], n.UID)
	le.PutUint32(b[40:], n.GID)
	le.PutUint32(b[44:], udfPerms(n.Mode))
	le.PutUint16(b[48:], uint16(n.Nlink))
	// recordFormat/recordDisplayAttr/recordLength = 0
	le.PutUint64(b[56:], dataLen)
	le.PutUint64(b[64:], dataBlocks)
	mt := n.ModTime
	if mt.IsZero() {
		mt = w.now
	}
	putTimestamp(b[72:], mt)
	putTimestamp(b[84:], mt)
	putTimestamp(b[96:], mt)
	le.PutUint32(b[108:], 1) // checkpoint
	// extendedAttrICB (112, long_ad) = 0
	putRegid(b[128:], "*fsforge", impSuffix())
	le.PutUint64(b[160:], w.uniqueID[n])
	le.PutUint32(b[168:], uint32(len(ea)))
	le.PutUint32(b[172:], uint32(len(ads)))
	copy(b[feHeaderLen:], ea)
	copy(b[feHeaderLen+len(ea):], ads)

	setTag(b, tagFE, 1, lbn, len(b)-16)
	return b
}

// shortADs encodes contiguous data starting at startLbn of dataLen bytes into
// short allocation descriptors, splitting at the per-extent limit.
func shortADs(startLbn uint32, dataLen uint64) []byte {
	if dataLen == 0 {
		return nil
	}
	const maxExt = (uint64(1)<<30 - blockSize) &^ (blockSize - 1)
	var out []byte
	lbn := startLbn
	for dataLen > 0 {
		n := dataLen
		if n > maxExt {
			n = maxExt
		}
		ad := make([]byte, 8)
		putShortAD(ad, uint32(n), lbn)
		out = append(out, ad...)
		blocks := (n + blockSize - 1) / blockSize
		lbn += uint32(blocks)
		dataLen -= n
	}
	return out
}

// deviceEA builds the extended-attribute area carrying a device node's numbers:
// an Extended Attribute Header Descriptor followed by a Device Specification.
func (w *uwriter) deviceEA(n *image.Node, lbn uint32) []byte {
	const eahdLen = 24
	const devLen = 24
	ea := make([]byte, eahdLen+devLen)

	// Device Specification attribute (ECMA-167 4/14.10.7).
	d := ea[eahdLen:]
	le.PutUint32(d[0:], 12) // attrType
	d[4] = 1                // attrSubtype
	le.PutUint32(d[8:], devLen)
	le.PutUint32(d[12:], 0) // impUseLength
	le.PutUint32(d[16:], uint32(n.Rdev>>8))
	le.PutUint32(d[20:], uint32(n.Rdev&0xff))

	// Extended Attribute Header Descriptor (ECMA-167 4/14.10.1).
	le.PutUint32(ea[16:], eahdLen+devLen) // impAttrLocation (none -> end)
	le.PutUint32(ea[20:], eahdLen+devLen) // appAttrLocation (none -> end)
	setTag(ea, 0x0106, 1, lbn, eahdLen-16)
	return ea
}

// buildDir renders a directory's data: a parent File Identifier Descriptor
// followed by one per child, sorted, as a contiguous byte stream over blocks
// starting at baseLbn (used to stamp each FID's tag location).
func (w *uwriter) buildDir(n *image.Node, baseLbn uint32) []byte {
	var out []byte
	add := func(name string, target *image.Node, chars uint8) {
		off := len(out)
		fid := w.buildFID(name, w.feLbn[target], chars, baseLbn+uint32(off/blockSize))
		out = append(out, fid...)
	}
	parent := w.parent[n]
	if parent == nil {
		parent = n // root's parent is itself
	}
	add("", parent, fidParent|fidDirectory)
	for _, e := range sortedChildren(n) {
		chars := uint8(0)
		if e.Node.IsDir() {
			chars = fidDirectory
		}
		add(e.Name, e.Node, chars)
	}
	return out
}

// buildFID renders one File Identifier Descriptor (ECMA-167 4/14.4), padded to
// a 4-byte boundary. An empty name marks the parent entry.
func (w *uwriter) buildFID(name string, icbLbn uint32, chars uint8, lbn uint32) []byte {
	var ident []byte
	if name != "" {
		ident = cs0Bytes(name)
	}
	total := 38 + len(ident)
	if pad := total % 4; pad != 0 {
		total += 4 - pad
	}
	b := make([]byte, total)
	le.PutUint16(b[16:], 1) // fileVersionNum
	b[18] = chars
	b[19] = byte(len(ident))
	putLongAD(b[20:], blockSize, icbLbn, 0) // icb -> child FE
	// lengthOfImpUse (36) = 0
	copy(b[38:], ident)
	setTag(b, tagFID, 1, lbn, total-16)
	return b
}

// buildSymlink renders a UDF symlink's data: a sequence of path components
// (ECMA-167 4/14.16.1) describing target.
func buildSymlink(target string) []byte {
	var out []byte
	comp := func(typ uint8, name string) {
		var ident []byte
		if name != "" {
			ident = cs0Bytes(name)
		}
		c := make([]byte, 4+len(ident))
		c[0] = typ
		c[1] = byte(len(ident))
		// componentFileVersionNum (2) = 0
		copy(c[4:], ident)
		out = append(out, c...)
	}
	rest := target
	if strings.HasPrefix(rest, "/") {
		comp(1, "") // root
		rest = strings.TrimPrefix(rest, "/")
	}
	for _, seg := range strings.Split(rest, "/") {
		switch seg {
		case "", ".":
			comp(4, "") // current / collapse empty
		case "..":
			comp(3, "")
		default:
			comp(5, seg)
		}
	}
	return out
}
