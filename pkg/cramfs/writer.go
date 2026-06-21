package cramfs

import (
	"bytes"
	"compress/zlib"
	"hash/crc32"
	"io"
	"io/fs"
	"sort"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// cwriter lays a node tree out as a cramfs image. Every inode lives inline in
// its parent directory's entries; a node's "content" — directory entries, or a
// regular file's / symlink's compressed block data — sits in a separate,
// 4-byte-aligned region whose offset the inode records.
type cwriter struct {
	dev   device.Device
	label string

	blocks   map[*image.Node][][]byte // compressed data blocks (files, symlinks)
	dataSize map[*image.Node]uint32   // bytes of a file's data region
	off      map[*image.Node]uint32   // content offset, or 0

	cursor      uint32
	totalBlocks uint32
	totalFiles  uint32
	err         error
}

func newCwriter(dev device.Device, label string) *cwriter {
	return &cwriter{
		dev:      dev,
		label:    label,
		blocks:   map[*image.Node][][]byte{},
		dataSize: map[*image.Node]uint32{},
		off:      map[*image.Node]uint32{},
		cursor:   superblockSize,
	}
}

func (w *cwriter) write(root *image.Node) error {
	w.prepare(root)
	w.assign(root)
	total := align4(w.cursor)

	w.writeNode(root)
	w.writeSuperblock(root, total)
	if w.err != nil {
		return w.err
	}
	return w.writeCRC(total)
}

// prepare compresses every regular file's and symlink's data and counts inodes.
func (w *cwriter) prepare(n *image.Node) {
	w.totalFiles++
	switch {
	case n.IsDir():
		for _, e := range sortedChildren(n) {
			w.prepare(e.Node)
		}
	case n.Mode&fs.ModeSymlink != 0:
		w.compressData(n, []byte(n.Link))
	case n.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
		// no data
	default:
		w.compressFile(n)
	}
}

func (w *cwriter) compressFile(n *image.Node) {
	if n.Content == nil || n.Content.Size() == 0 {
		return
	}
	size := n.Content.Size()
	buf := make([]byte, blockSize)
	for off := int64(0); off < size; off += blockSize {
		nb := int64(blockSize)
		if rem := size - off; rem < nb {
			nb = rem
		}
		if _, err := n.Content.ReadAt(buf[:nb], off); err != nil && err != io.EOF {
			w.err = err
		}
		w.addBlock(n, zlibBlock(buf[:nb]))
	}
}

// compressData compresses a small in-memory payload (a symlink target).
func (w *cwriter) compressData(n *image.Node, data []byte) {
	if len(data) == 0 {
		return
	}
	for off := 0; off < len(data); off += blockSize {
		end := off + blockSize
		if end > len(data) {
			end = len(data)
		}
		w.addBlock(n, zlibBlock(data[off:end]))
	}
}

// addBlock records a compressed block and keeps the data-region size in step:
// the block-pointer array (4 bytes per block) plus the compressed bytes.
func (w *cwriter) addBlock(n *image.Node, blk []byte) {
	w.blocks[n] = append(w.blocks[n], blk)
	w.dataSize[n] += 4 + uint32(len(blk))
	w.totalBlocks++
}

// assign places every node's content region at a 4-byte-aligned offset, in a
// deterministic pre-order walk.
func (w *cwriter) assign(n *image.Node) {
	w.cursor = align4(w.cursor)
	if n.IsDir() {
		if len(n.Children) > 0 {
			w.off[n] = w.cursor
			w.cursor += entriesSize(n)
		}
		for _, e := range sortedChildren(n) {
			w.assign(e.Node)
		}
		return
	}
	if w.dataSize[n] > 0 {
		w.off[n] = w.cursor
		w.cursor += w.dataSize[n]
	}
}

// writeNode writes a node's content (directory entries or file data) and
// recurses into directories.
func (w *cwriter) writeNode(n *image.Node) {
	switch {
	case n.IsDir():
		if len(n.Children) > 0 {
			pos := w.off[n]
			for _, e := range sortedChildren(n) {
				w.writeAt(w.childInode(e.Name, e.Node).marshal(), int64(pos))
				pos += inodeSize
				name := paddedName(e.Name)
				w.writeAt(name, int64(pos))
				pos += uint32(len(name))
			}
		}
		for _, e := range sortedChildren(n) {
			w.writeNode(e.Node)
		}
	default:
		if w.dataSize[n] > 0 {
			w.writeAt(w.dataRegion(n), int64(w.off[n]))
		}
	}
}

// childInode builds the inline inode a parent stores for child named name.
func (w *cwriter) childInode(name string, n *image.Node) cinode {
	return cinode{
		mode:    modeToUnix(n.Mode),
		uid:     n.UID,
		gid:     n.GID,
		size:    w.sizeField(n),
		namelen: uint32(len(paddedName(name)) / 4),
		offset:  w.off[n] >> 2,
	}
}

// sizeField is the inode size field: a directory's entries length, a file's or
// symlink's byte length, a device's rdev, or zero.
func (w *cwriter) sizeField(n *image.Node) uint32 {
	switch {
	case n.IsDir():
		return entriesSize(n)
	case n.Mode&fs.ModeSymlink != 0:
		return uint32(len(n.Link))
	case n.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		return uint32(n.Rdev) & 0xffffff
	case n.Mode&(fs.ModeNamedPipe|fs.ModeSocket) != 0:
		return 0
	default:
		if n.Content != nil {
			return uint32(n.Content.Size())
		}
		return 0
	}
}

// dataRegion assembles a file's data region: the block-pointer array (each entry
// the absolute end offset of its compressed block) followed by the blocks.
func (w *cwriter) dataRegion(n *image.Node) []byte {
	blks := w.blocks[n]
	region := make([]byte, w.dataSize[n])
	pos := uint32(len(blks) * 4)
	for i, blk := range blks {
		copy(region[pos:], blk)
		pos += uint32(len(blk))
		le.PutUint32(region[i*4:], w.off[n]+pos)
	}
	return region
}

func (w *cwriter) writeSuperblock(root *image.Node, total uint32) {
	b := make([]byte, superblockSize)
	le.PutUint32(b[0:], magic)
	le.PutUint32(b[4:], total)
	le.PutUint32(b[8:], flagFSIDv2|flagSortedDirs|flagShiftedRoot)
	// future (12) = 0
	copy(b[16:32], signature)
	// fsid.crc (32) filled by writeCRC
	// fsid.edition (36) = 0
	le.PutUint32(b[40:], w.totalBlocks)
	le.PutUint32(b[44:], w.totalFiles)
	copy(b[48:64], w.label)
	copy(b[rootInodeOffset:], w.childInode("", root).marshal())
	w.writeAt(b, 0)
}

// writeCRC streams the whole image back through crc32 (with the crc field left
// zero) and stores the result.
func (w *cwriter) writeCRC(total uint32) error {
	h := crc32.NewIEEE()
	buf := make([]byte, 64<<10)
	for off := int64(0); off < int64(total); {
		n := int64(len(buf))
		if rem := int64(total) - off; rem < n {
			n = rem
		}
		if _, err := w.dev.ReadAt(buf[:n], off); err != nil && err != io.EOF {
			return err
		}
		h.Write(buf[:n])
		off += n
	}
	var crc [4]byte
	le.PutUint32(crc[:], h.Sum32())
	_, err := w.dev.WriteAt(crc[:], crcOffset)
	return err
}

func (w *cwriter) writeAt(p []byte, off int64) {
	if w.err != nil {
		return
	}
	if _, err := w.dev.WriteAt(p, off); err != nil {
		w.err = err
	}
}

// --- helpers ---

// zlibBlock returns the zlib stream for one (<= 4 KiB) data block.
func zlibBlock(data []byte) []byte {
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write(data)
	zw.Close()
	return b.Bytes()
}

// entriesSize is the byte length of a directory's entries region.
func entriesSize(n *image.Node) uint32 {
	var total uint32
	for _, e := range n.Children {
		total += inodeSize + uint32(len(paddedName(e.Name)))
	}
	return total
}

// paddedName returns name as bytes padded with NULs to a 4-byte boundary.
func paddedName(name string) []byte {
	n := (len(name) + 3) &^ 3
	b := make([]byte, n)
	copy(b, name)
	return b
}

func align4(v uint32) uint32 { return (v + 3) &^ 3 }

func sortedChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
