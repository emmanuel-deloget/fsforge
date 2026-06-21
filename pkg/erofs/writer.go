package erofs

import (
	"io"
	"io/fs"
	"sort"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// ewriter lays a node tree out as an EROFS image. The metadata area (inodes)
// occupies block 0 onward — sharing block 0 with the superblock at offset 1024
// — and the data area (file/dir/symlink blocks) follows on the next block
// boundary. Two passes keep it simple: assignNids numbers every inode so
// directories can reference their children, then layout writes data and inodes.
type ewriter struct {
	dev   device.Device
	clock image.Clock

	nids     map[*image.Node]uint64 // node -> on-disk nid (32-byte units)
	laid     map[*image.Node]bool   // node -> already written (hard links)
	nextOff  int64                  // metadata byte cursor, for nid assignment
	inoCount uint32

	dataStart  uint32 // first data block
	dataCursor uint32 // next free data block

	err error // sticky device-write error
}

func newEwriter(dev device.Device, clock image.Clock) *ewriter {
	return &ewriter{
		dev:     dev,
		clock:   clock,
		nids:    make(map[*image.Node]uint64),
		laid:    make(map[*image.Node]bool),
		nextOff: align(superOffset+superSize, inodeExtendedSize), // 1152
	}
}

// align rounds v up to the next multiple of a (a a power of two).
func align(v, a int64) int64 { return (v + a - 1) &^ (a - 1) }

// assignNids numbers every unique node pre-order (sorted children) so a
// directory's dirents can reference children written later. Extended inodes are
// 64 bytes; starting 64-aligned in a 4 KiB block, none ever straddle a block.
func (w *ewriter) assignNids(n *image.Node) {
	if _, ok := w.nids[n]; ok {
		return
	}
	w.inoCount++
	n.Ino = w.inoCount
	w.nids[n] = uint64(w.nextOff) / nidSlot
	w.nextOff += inodeExtendedSize
	if n.IsDir() {
		for _, e := range sortChildren(n) {
			w.assignNids(e.Node)
		}
	}
}

// layout writes a node's data blocks and its inode core, recursing into
// directories. Hard-linked nodes are written once.
func (w *ewriter) layout(n *image.Node, parentNid uint64) error {
	if w.laid[n] {
		return nil
	}
	w.laid[n] = true

	switch {
	case n.IsDir():
		return w.writeDir(n, parentNid)
	case n.Mode&fs.ModeSymlink != 0:
		w.writeSymlink(n)
	case n.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		w.writeInode(n, 0, newEncodeDev(n.Rdev))
	case n.Mode&(fs.ModeNamedPipe|fs.ModeSocket) != 0:
		w.writeInode(n, 0, 0)
	default:
		w.writeFile(n)
	}
	return nil
}

func (w *ewriter) writeFile(n *image.Node) {
	var size int64
	if n.Content != nil {
		size = n.Content.Size()
	}
	raw := uint32(0)
	if size > 0 {
		raw = w.dataCursor
		w.writeContent(n.Content, size)
	}
	w.writeInode(n, size, raw)
}

func (w *ewriter) writeSymlink(n *image.Node) {
	target := []byte(n.Link)
	raw := uint32(0)
	if len(target) > 0 {
		raw = w.dataCursor
		w.writeBlocks(target)
	}
	w.writeInode(n, int64(len(target)), raw)
}

func (w *ewriter) writeDir(n *image.Node, parentNid uint64) error {
	children := sortChildren(n)
	entries := make([]dentry, 0, len(children)+2)
	entries = append(entries,
		dentry{name: ".", nid: w.nids[n], ftype: ftDir},
		dentry{name: "..", nid: parentNid, ftype: ftDir})
	for _, e := range children {
		entries = append(entries, dentry{
			name:  e.Name,
			nid:   w.nids[e.Node],
			ftype: fileType(e.Node.Mode),
		})
	}

	data := packDir(entries)
	raw := w.dataCursor
	w.writeBlocks(data)
	w.writeInode(n, int64(len(data)), raw)

	for _, e := range children {
		if err := w.layout(e.Node, w.nids[n]); err != nil {
			return err
		}
	}
	return nil
}

// writeContent streams a lazy source into the data area, block by block.
func (w *ewriter) writeContent(c tree.Source, size int64) {
	buf := make([]byte, blockSize)
	for off := int64(0); off < size; off += blockSize {
		nb := int64(blockSize)
		if rem := size - off; rem < nb {
			nb = rem
		}
		for i := range buf {
			buf[i] = 0
		}
		if _, err := c.ReadAt(buf[:nb], off); err != nil && err != io.EOF {
			w.err = err
		}
		w.writeAt(buf, int64(w.dataCursor)*blockSize)
		w.dataCursor++
	}
}

// writeBlocks writes data (directory listing or symlink target) into the data
// area as whole zero-padded blocks.
func (w *ewriter) writeBlocks(data []byte) {
	for off := 0; off < len(data); off += blockSize {
		end := off + blockSize
		if end > len(data) {
			end = len(data)
		}
		block := make([]byte, blockSize)
		copy(block, data[off:end])
		w.writeAt(block, int64(w.dataCursor)*blockSize)
		w.dataCursor++
	}
}

func (w *ewriter) writeInode(n *image.Node, size int64, union uint32) {
	mt := n.ModTime
	if mt.IsZero() {
		mt = w.clock.Now()
	}
	in := dinode{
		mode:  modeToUnix(n.Mode),
		size:  uint64(size),
		union: union,
		ino:   n.Ino,
		uid:   n.UID,
		gid:   n.GID,
		mtime: uint64(mt.Unix()),
		nsec:  uint32(mt.Nanosecond()),
		nlink: uint32(n.Nlink),
	}
	w.writeAt(in.marshal(), int64(w.nids[n])*nidSlot)
}

func (w *ewriter) writeAt(p []byte, off int64) {
	if w.err != nil {
		return
	}
	if _, err := w.dev.WriteAt(p, off); err != nil {
		w.err = err
	}
}

func sortChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
