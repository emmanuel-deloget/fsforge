package squashfs

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"sort"

	"github.com/emmanuel-deloget/fsforge/pkg/compress"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

var le = binary.LittleEndian

// childRes records where a written node's inode landed, so a parent directory
// can reference it.
type childRes struct {
	block  uint32 // inode metadata-block start (relative to inode table)
	offset uint16 // offset within the uncompressed block
	ino    uint32
	typ    uint16
}

type swriter struct {
	dev       device.Device
	comp      compress.Compressor
	blockSize uint32
	clock     image.Clock

	pos int64 // current archive write position
	err error // sticky device-write error

	inodes  metaWriter
	dirs    metaWriter
	ids     []uint32
	idIndex map[uint32]uint16
	written map[*image.Node]childRes
	nextIno uint32
}

func newSwriter(dev device.Device, comp compress.Compressor, blockSize uint32, clock image.Clock) *swriter {
	return &swriter{
		dev:       dev,
		comp:      comp,
		blockSize: blockSize,
		clock:     clock,
		pos:       superblockSize,
		inodes:    metaWriter{comp: comp},
		dirs:      metaWriter{comp: comp},
		idIndex:   make(map[uint32]uint16),
		written:   make(map[*image.Node]childRes),
		nextIno:   1,
	}
}

func (w *swriter) writeAt(p []byte) {
	if w.err != nil {
		return
	}
	if _, err := w.dev.WriteAt(p, w.pos); err != nil {
		w.err = err
		return
	}
	w.pos += int64(len(p))
}

func (w *swriter) idOf(v uint32) uint16 {
	if i, ok := w.idIndex[v]; ok {
		return i
	}
	i := uint16(len(w.ids))
	w.ids = append(w.ids, v)
	w.idIndex[v] = i
	return i
}

// assignInos numbers every node pre-order (parents before children) so a child
// directory inode can record its parent's number even though writing happens
// post-order.
func (w *swriter) assignInos(n *image.Node) {
	if n.Ino != 0 {
		return
	}
	n.Ino = w.nextIno
	w.nextIno++
	if n.IsDir() {
		for _, e := range sortChildren(n) {
			w.assignInos(e.Node)
		}
	}
}

func (w *swriter) header(typ uint16, n *image.Node) []byte {
	b := make([]byte, 16)
	le.PutUint16(b[0:], typ)
	le.PutUint16(b[2:], unixPerm(n.Mode))
	le.PutUint16(b[4:], w.idOf(n.UID))
	le.PutUint16(b[6:], w.idOf(n.GID))
	le.PutUint32(b[8:], uint32(n.ModTime.Unix()))
	le.PutUint32(b[12:], n.Ino)
	return b
}

func (w *swriter) emitInode(body []byte, n *image.Node, typ uint16) childRes {
	block, offset := w.inodes.ref()
	w.inodes.write(body)
	res := childRes{block: block, offset: offset, ino: n.Ino, typ: typ}
	w.written[n] = res
	return res
}

// writeNode lays out a node post-order and returns its inode location.
func (w *swriter) writeNode(n *image.Node, parentIno uint32) (childRes, error) {
	if r, ok := w.written[n]; ok {
		return r, nil // shared (hard-linked) node already written
	}
	switch {
	case n.IsDir():
		return w.writeDir(n, parentIno)
	case n.Mode&fs.ModeSymlink != 0:
		return w.writeSymlink(n), nil
	case n.Mode&fs.ModeCharDevice != 0:
		return w.writeDevice(n, typeChrdev), nil
	case n.Mode&fs.ModeDevice != 0:
		return w.writeDevice(n, typeBlkdev), nil
	case n.Mode&fs.ModeNamedPipe != 0:
		return w.writeIPC(n, typeFifo), nil
	case n.Mode&fs.ModeSocket != 0:
		return w.writeIPC(n, typeSocket), nil
	default:
		return w.writeFile(n), nil
	}
}

func (w *swriter) writeFile(n *image.Node) childRes {
	start := uint32(w.pos)
	var size int64
	if n.Content != nil {
		size = n.Content.Size()
	}
	var sizes []uint32
	buf := make([]byte, w.blockSize)
	for off := int64(0); off < size; off += int64(w.blockSize) {
		nb := int64(w.blockSize)
		if rem := size - off; rem < nb {
			nb = rem
		}
		if n.Content != nil {
			if _, err := n.Content.ReadAt(buf[:nb], off); err != nil && err != io.EOF {
				w.err = err
			}
		}
		payload, szField := dataBlock(w.comp, buf[:nb])
		w.writeAt(payload)
		sizes = append(sizes, szField)
	}

	body := w.header(typeFile, n)
	meta := make([]byte, 16)
	le.PutUint32(meta[0:], start)
	le.PutUint32(meta[4:], noFragment)
	le.PutUint32(meta[8:], 0) // offset into fragment (unused)
	le.PutUint32(meta[12:], uint32(size))
	body = append(body, meta...)
	for _, s := range sizes {
		var x [4]byte
		le.PutUint32(x[:], s)
		body = append(body, x[:]...)
	}
	return w.emitInode(body, n, typeFile)
}

func (w *swriter) writeSymlink(n *image.Node) childRes {
	target := []byte(n.Link)
	body := w.header(typeSymlink, n)
	meta := make([]byte, 8)
	le.PutUint32(meta[0:], uint32(n.Nlink))
	le.PutUint32(meta[4:], uint32(len(target)))
	body = append(body, meta...)
	body = append(body, target...)
	return w.emitInode(body, n, typeSymlink)
}

func (w *swriter) writeDevice(n *image.Node, typ uint16) childRes {
	body := w.header(typ, n)
	meta := make([]byte, 8)
	le.PutUint32(meta[0:], uint32(n.Nlink))
	le.PutUint32(meta[4:], uint32(n.Rdev))
	body = append(body, meta...)
	return w.emitInode(body, n, typ)
}

func (w *swriter) writeIPC(n *image.Node, typ uint16) childRes {
	body := w.header(typ, n)
	meta := make([]byte, 4)
	le.PutUint32(meta[0:], uint32(n.Nlink))
	body = append(body, meta...)
	return w.emitInode(body, n, typ)
}

type entry struct {
	name string
	res  childRes
}

func (w *swriter) writeDir(n *image.Node, parentIno uint32) (childRes, error) {
	children := sortChildren(n)
	entries := make([]entry, 0, len(children))
	subdirs := 0
	for _, e := range children {
		res, err := w.writeNode(e.Node, n.Ino)
		if err != nil {
			return childRes{}, err
		}
		entries = append(entries, entry{name: e.Name, res: res})
		if e.Node.IsDir() {
			subdirs++
		}
	}

	listing := buildListing(entries)
	if len(listing)+3 > 0xFFFF {
		return childRes{}, fmt.Errorf("squashfs: directory too large for a basic inode (%d entries)", len(entries))
	}
	dblock, doffset := w.dirs.ref()
	w.dirs.write(listing)

	parent := parentIno
	if parent == 0 { // root: parent is the conventional inodes_count+1
		parent = n.Ino + 1
	}
	body := w.header(typeDir, n)
	meta := make([]byte, 16)
	le.PutUint32(meta[0:], dblock)
	le.PutUint32(meta[4:], uint32(2+subdirs))
	le.PutUint16(meta[8:], uint16(len(listing)+3))
	le.PutUint16(meta[10:], doffset)
	le.PutUint32(meta[12:], parent)
	body = append(body, meta...)
	return w.emitInode(body, n, typeDir), nil
}

// buildListing serialises a directory's entries into squashfs directory headers,
// starting a new header whenever the referenced inode metadata block changes,
// the 256-entry limit is hit, or the signed inode-number delta would overflow.
func buildListing(entries []entry) []byte {
	var out []byte
	for i := 0; i < len(entries); {
		base := entries[i].res.ino
		block := entries[i].res.block
		j := i
		for j < len(entries) && j-i < 256 {
			d := int64(entries[j].res.ino) - int64(base)
			if entries[j].res.block != block || d < -32768 || d > 32767 {
				break
			}
			j++
		}
		var h [12]byte
		le.PutUint32(h[0:], uint32(j-i-1))
		le.PutUint32(h[4:], block)
		le.PutUint32(h[8:], base)
		out = append(out, h[:]...)
		for _, e := range entries[i:j] {
			var eh [8]byte
			le.PutUint16(eh[0:], e.res.offset)
			le.PutUint16(eh[2:], uint16(int16(int64(e.res.ino)-int64(base))))
			le.PutUint16(eh[4:], e.res.typ)
			le.PutUint16(eh[6:], uint16(len(e.name)-1))
			out = append(out, eh[:]...)
			out = append(out, e.name...)
		}
		i = j
	}
	return out
}

// writeIDTable writes the uid/gid metadata blocks followed by the u64 index,
// returning the index location and the number of ids.
func (w *swriter) writeIDTable() (uint64, uint16) {
	var blockOffsets []uint64
	for i := 0; i < len(w.ids); i += idsPerMetaBlock {
		end := min(i+idsPerMetaBlock, len(w.ids))
		raw := make([]byte, (end-i)*4)
		for k, v := range w.ids[i:end] {
			le.PutUint32(raw[k*4:], v)
		}
		blockOffsets = append(blockOffsets, uint64(w.pos))
		w.writeAt(metaBlock(w.comp, raw))
	}
	idTableStart := uint64(w.pos)
	idx := make([]byte, 8*len(blockOffsets))
	for k, off := range blockOffsets {
		le.PutUint64(idx[k*8:], off)
	}
	w.writeAt(idx)
	return idTableStart, uint16(len(w.ids))
}

func sortChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func unixPerm(m fs.FileMode) uint16 {
	p := uint16(m.Perm())
	if m&fs.ModeSetuid != 0 {
		p |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		p |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		p |= 0o1000
	}
	return p
}
