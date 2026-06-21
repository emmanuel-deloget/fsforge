package romfs

import (
	"io"
	"io/fs"
	"sort"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// rwriter lays a node tree out as a romfs image. Every node has a 16-byte file
// header (followed by its name and, for files/symlinks, its data); a directory's
// children form a linked list — ".", ".." then the sorted real children — that
// the directory's `spec` field points at.
type rwriter struct {
	dev   device.Device
	label string

	hdrOff    map[*image.Node]uint32 // node -> its inode header offset
	dotOff    map[*image.Node]uint32 // directory -> "." entry offset (== children list start)
	dotdotOff map[*image.Node]uint32 // directory -> ".." entry offset
	parentHdr map[*image.Node]uint32 // directory -> parent inode-header offset

	rootHdr uint32
	size    uint32
	err     error
}

func newRwriter(dev device.Device, label string) *rwriter {
	return &rwriter{
		dev: dev, label: label,
		hdrOff:    map[*image.Node]uint32{},
		dotOff:    map[*image.Node]uint32{},
		dotdotOff: map[*image.Node]uint32{},
		parentHdr: map[*image.Node]uint32{},
	}
}

func (w *rwriter) write(root *image.Node) error {
	// The root inode header sits right after the superblock and its volume name.
	w.rootHdr = headerSize + paddedName(w.label)
	rootHdrLen := headerSize + paddedName(".") // root header is named "."
	w.size = w.layoutDir(root, w.rootHdr, w.rootHdr+rootHdrLen, w.rootHdr)

	w.writeSuperblock()
	w.writeRootHeader(root)
	w.writeDir(root)
	if w.err != nil {
		return w.err
	}
	return w.finishChecksum()
}

// layoutDir assigns offsets for directory dir (header at hdrOff, children list
// beginning at listStart, parent header at parentHdr) and returns the next free
// offset after the whole subtree.
func (w *rwriter) layoutDir(dir *image.Node, hdrOff, listStart, parentHdr uint32) uint32 {
	w.hdrOff[dir] = hdrOff
	w.parentHdr[dir] = parentHdr
	w.dotOff[dir] = listStart
	w.dotdotOff[dir] = listStart + headerSize + paddedName(".")
	off := w.dotdotOff[dir] + headerSize + paddedName("..")

	children := sortedChildren(dir)
	for _, e := range children {
		w.hdrOff[e.Node] = off
		off += headerSize + paddedName(e.Name)
		if sz := w.dataLen(e.Node); sz > 0 {
			off += alignUp(sz)
		}
	}
	for _, e := range children {
		if e.Node.IsDir() {
			off = w.layoutDir(e.Node, w.hdrOff[e.Node], off, hdrOff)
		}
	}
	return off
}

func (w *rwriter) writeSuperblock() {
	n := headerSize + int(paddedName(w.label))
	b := make([]byte, n)
	be.PutUint32(b[0:], magicW0)
	be.PutUint32(b[4:], magicW1)
	be.PutUint32(b[8:], w.size)
	// checksum (12) filled by finishChecksum
	copy(b[16:], w.label)
	w.writeAt(b, 0)
}

func (w *rwriter) writeRootHeader(root *image.Node) {
	// The root header is a directory named "." whose next pointer terminates the
	// (single-entry) top level and whose spec points to its children list.
	b := make([]byte, headerSize+int(paddedName(".")))
	putHeader(b, 0, typeDir|flagExec, w.dotOff[root], 0)
	copy(b[16:], ".")
	fixChecksum(b, 12, len(b))
	w.writeAt(b, int64(w.rootHdr))
}

// writeDir writes a directory's children list (".", ".." and the sorted real
// children, with their data) and recurses into subdirectories.
func (w *rwriter) writeDir(dir *image.Node) {
	children := sortedChildren(dir)

	firstChild := uint32(0)
	if len(children) > 0 {
		firstChild = w.hdrOff[children[0].Node]
	}
	w.writeEntry(w.dotOff[dir], ".", typeHardlink, false, w.dotdotOff[dir], w.hdrOff[dir], 0, nil)
	w.writeEntry(w.dotdotOff[dir], "..", typeHardlink, false, firstChild, w.parentHdr[dir], 0, nil)

	for i, e := range children {
		n := e.Node
		next := uint32(0)
		if i+1 < len(children) {
			next = w.hdrOff[children[i+1].Node]
		}
		typ, exec := typeOf(n.Mode)
		spec, size, data := w.payload(n)
		dataOff := w.writeEntry(w.hdrOff[n], e.Name, typ, exec, next, spec, size, data)
		if typ == typeReg && size > 0 {
			w.streamContent(dataOff, n)
		}
	}
	for _, e := range children {
		if e.Node.IsDir() {
			w.writeDir(e.Node)
		}
	}
}

// payload returns a node's spec field, size field and inline data (symlinks).
func (w *rwriter) payload(n *image.Node) (spec, size uint32, data []byte) {
	switch {
	case n.IsDir():
		return w.dotOff[n], 0, nil
	case n.Mode&fs.ModeSymlink != 0:
		return 0, uint32(len(n.Link)), []byte(n.Link)
	case n.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		major := uint32(n.Rdev >> 8)
		minor := uint32(n.Rdev & 0xff)
		return major<<16 | minor, 0, nil
	case n.Mode&(fs.ModeNamedPipe|fs.ModeSocket) != 0:
		return 0, 0, nil
	default:
		return 0, w.dataLen(n), nil // regular file; data streamed separately
	}
}

// writeEntry writes one file header at off: the 16-byte header, the padded name
// and, for symlinks, the inline data. It returns the offset where regular-file
// data should follow (streamed separately, never held in memory).
func (w *rwriter) writeEntry(off uint32, name string, typ uint32, exec bool, next, spec, size uint32, data []byte) uint32 {
	flags := typ
	if exec {
		flags |= flagExec
	}
	nameLen := int(paddedName(name))
	b := make([]byte, headerSize+nameLen)
	putHeader(b, next, flags, spec, size)
	copy(b[headerSize:], name)
	fixChecksum(b, 12, len(b))
	w.writeAt(b, int64(off))

	dataOff := off + uint32(headerSize) + uint32(nameLen)
	if data != nil {
		w.writePadded(dataOff, data)
	}
	return dataOff
}

// streamContent copies a regular file's contents into the data region at off,
// in chunks (never buffering the whole file), then zero-pads to 16 bytes.
func (w *rwriter) streamContent(off uint32, n *image.Node) {
	size := int64(n.Content.Size())
	buf := make([]byte, 64<<10)
	for pos := int64(0); pos < size; {
		nb := int64(len(buf))
		if rem := size - pos; rem < nb {
			nb = rem
		}
		if _, err := n.Content.ReadAt(buf[:nb], pos); err != nil && err != io.EOF {
			w.err = err
		}
		w.writeAt(buf[:nb], int64(off)+pos)
		pos += nb
	}
	if pad := int64(alignUp(uint32(size))) - size; pad > 0 {
		w.writeAt(make([]byte, pad), int64(off)+size)
	}
}

// dataLen is the byte length of a node's data region (regular files, symlinks).
func (w *rwriter) dataLen(n *image.Node) uint32 {
	switch {
	case n.Mode&fs.ModeSymlink != 0:
		return uint32(len(n.Link))
	case n.IsDir(), n.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
		return 0
	default:
		if n.Content != nil {
			return uint32(n.Content.Size())
		}
		return 0
	}
}

func (w *rwriter) writePadded(off uint32, data []byte) {
	padded := make([]byte, alignUp(uint32(len(data))))
	copy(padded, data)
	w.writeAt(padded, int64(off))
}

func (w *rwriter) writeAt(p []byte, off int64) {
	if w.err != nil {
		return
	}
	if _, err := w.dev.WriteAt(p, off); err != nil {
		w.err = err
	}
}

// finishChecksum streams the first 512 bytes (or the whole image when smaller)
// back and stores the superblock checksum that makes those words sum to zero.
func (w *rwriter) finishChecksum() error {
	n := int64(512)
	if int64(w.size) < n {
		n = int64(w.size)
	}
	buf := make([]byte, n)
	if _, err := w.dev.ReadAt(buf, 0); err != nil && err != io.EOF {
		return err
	}
	be.PutUint32(buf[12:], 0)
	var crc [4]byte
	be.PutUint32(crc[:], checksum(buf))
	_, err := w.dev.WriteAt(crc[:], 12)
	return err
}

func sortedChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
