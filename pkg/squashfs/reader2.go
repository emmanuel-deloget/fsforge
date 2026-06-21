package squashfs

import (
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// readInode parses the inode at ref into a node, recursing into directories.
// Shared refs (hard links) are returned once.
func (r *squashReader) readInode(ref uint64, seen map[uint64]*image.Node) (*image.Node, error) {
	if n, ok := seen[ref]; ok {
		return n, nil
	}
	pos, err := r.inodeAt(ref)
	if err != nil {
		return nil, err
	}
	d := r.inodeData
	if pos+16 > len(d) {
		return nil, io.ErrUnexpectedEOF
	}
	typ := le.Uint16(d[pos:])
	perm := le.Uint16(d[pos+2:])
	uidIdx := le.Uint16(d[pos+4:])
	gidIdx := le.Uint16(d[pos+6:])
	mtime := le.Uint32(d[pos+8:])
	body := pos + 16

	meta := tree.Meta{
		Mode:    sqMode(typ, perm),
		UID:     r.idAt(uidIdx),
		GID:     r.idAt(gidIdx),
		ModTime: time.Unix(int64(mtime), 0).UTC(),
	}
	n := &image.Node{Inode: tree.Inode{Meta: meta}, Nlink: 1}
	seen[ref] = n

	switch typ {
	case typeDir:
		startBlock := le.Uint32(d[body:])
		n.Nlink = int(le.Uint32(d[body+4:]))
		fileSize := int(le.Uint16(d[body+8:]))
		offset := int(le.Uint16(d[body+10:]))
		if err := r.readDir(n, startBlock, offset, fileSize, seen); err != nil {
			return nil, err
		}
	case 8: // extended directory
		n.Nlink = int(le.Uint32(d[body+4:]))
		fileSize := int(le.Uint32(d[body+8:]))
		startBlock := le.Uint32(d[body+12:])
		offset := int(le.Uint16(d[body+16:]))
		if err := r.readDir(n, startBlock, offset, fileSize, seen); err != nil {
			return nil, err
		}
	case typeFile:
		n.Content = r.basicFile(d, body)
	case 9: // extended file
		n.Content, n.Nlink = r.extFile(d, body)
	case typeSymlink, 10:
		n.Nlink = int(le.Uint32(d[body:]))
		tgtSize := int(le.Uint32(d[body+4:]))
		n.Link = string(d[body+8 : body+8+tgtSize])
	case typeChrdev, typeBlkdev, 11, 12:
		n.Nlink = int(le.Uint32(d[body:]))
		n.Rdev = uint64(le.Uint32(d[body+4:]))
	case typeFifo, typeSocket, 13, 14:
		n.Nlink = int(le.Uint32(d[body:]))
	default:
		return nil, fmt.Errorf("squashfs: unknown inode type %d", typ)
	}
	return n, nil
}

func (r *squashReader) readDir(n *image.Node, startBlock uint32, offset, fileSize int, seen map[uint64]*image.Node) error {
	if fileSize <= 3 {
		return nil // empty directory
	}
	base, ok := r.dirMap[startBlock]
	if !ok {
		return fmt.Errorf("squashfs: bad directory block ref %d", startBlock)
	}
	listing := r.dirData[base+offset : base+offset+fileSize-3]
	for p := 0; p+12 <= len(listing); {
		count := int(le.Uint32(listing[p:])) + 1
		hdrStart := le.Uint32(listing[p+4:])
		p += 12
		for i := 0; i < count && p+8 <= len(listing); i++ {
			entOff := le.Uint16(listing[p:])
			nameLen := int(le.Uint16(listing[p+6:])) + 1
			name := string(listing[p+8 : p+8+nameLen])
			p += 8 + nameLen
			child, err := r.readInode(inodeRef(hdrStart, entOff), seen)
			if err != nil {
				return err
			}
			n.Children = append(n.Children, image.Entry{Name: name, Node: child})
		}
	}
	return nil
}

func (r *squashReader) basicFile(d []byte, body int) tree.Source {
	start := int64(le.Uint32(d[body:]))
	fragIdx := le.Uint32(d[body+4:])
	fragOff := le.Uint32(d[body+8:])
	size := int64(le.Uint32(d[body+12:]))
	return r.fileSource(start, fragIdx, fragOff, size, d, body+16)
}

func (r *squashReader) extFile(d []byte, body int) (tree.Source, int) {
	start := int64(le.Uint64(d[body:]))
	size := int64(le.Uint64(d[body+8:]))
	nlink := int(le.Uint32(d[body+24:]))
	fragIdx := le.Uint32(d[body+28:])
	fragOff := le.Uint32(d[body+32:])
	return r.fileSource(start, fragIdx, fragOff, size, d, body+40), nlink
}

func (r *squashReader) fileSource(start int64, fragIdx, fragOff uint32, size int64, d []byte, blockSizesAt int) tree.Source {
	bs := int64(r.sb.blockSize)
	var nFull int
	if fragIdx == noFragment {
		nFull = int((size + bs - 1) / bs)
	} else {
		nFull = int(size / bs)
	}
	sizes := make([]uint32, nFull)
	for i := 0; i < nFull; i++ {
		sizes[i] = le.Uint32(d[blockSizesAt+i*4:])
	}
	fsrc := &sqFile{r: r, start: start, size: size, blockSize: int(bs), sizes: sizes}
	if fragIdx != noFragment && int(fragIdx) < len(r.frags) {
		fsrc.hasFrag = true
		fsrc.frag = r.frags[fragIdx]
		fsrc.fragOff = int(fragOff)
		fsrc.tailLen = int(size) - nFull*int(bs)
	}
	return fsrc
}

// sqFile is a lazy tree.Source over a squashfs file, decompressing per block.
type sqFile struct {
	r         *squashReader
	start     int64
	size      int64
	blockSize int
	sizes     []uint32 // on-disk sizes of full data blocks (with flag bit)

	hasFrag bool
	frag    fragEntry
	fragOff int
	tailLen int
}

func (f *sqFile) Size() int64 { return f.size }

func (f *sqFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("squashfs: negative offset")
	}
	if off >= f.size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && off < f.size {
		block, within, err := f.blockAt(off)
		if err != nil {
			return n, err
		}
		c := copy(p[n:], block[within:])
		n += c
		off += int64(c)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// blockAt returns the decompressed block containing byte off, and the offset
// within it.
func (f *sqFile) blockAt(off int64) ([]byte, int, error) {
	idx := int(off / int64(f.blockSize))
	within := int(off % int64(f.blockSize))
	if idx < len(f.sizes) {
		// Locate the compressed block in the archive.
		pos := f.start
		for i := 0; i < idx; i++ {
			pos += int64(f.sizes[i] & 0xFFFFFF)
		}
		block, err := f.r.readDataBlock(pos, f.sizes[idx], f.blockSize)
		return block, within, err
	}
	// Fragment tail.
	if !f.hasFrag {
		return nil, 0, io.EOF
	}
	frag, err := f.r.readDataBlock(f.frag.start, f.frag.size, 0)
	if err != nil {
		return nil, 0, err
	}
	tail := frag[f.fragOff : f.fragOff+f.tailLen]
	return tail, within, nil
}

// readDataBlock reads and decompresses a data block. sizeField carries the
// on-disk size and the uncompressed flag; sizeField==0 is a sparse full block.
func (r *squashReader) readDataBlock(pos int64, sizeField uint32, fullSize int) ([]byte, error) {
	onDisk := int(sizeField & 0xFFFFFF)
	if onDisk == 0 {
		return make([]byte, fullSize), nil // sparse block of zeros
	}
	raw := make([]byte, onDisk)
	if _, err := r.dev.ReadAt(raw, pos); err != nil && err != io.EOF {
		return nil, err
	}
	if sizeField&blockUncompressed != 0 {
		return raw, nil
	}
	return r.comp.Decompress(nil, raw)
}

func sqMode(typ, perm uint16) fs.FileMode {
	m := fs.FileMode(perm & 0o777)
	if perm&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if perm&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if perm&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	switch typ {
	case typeDir, 8:
		m |= fs.ModeDir
	case typeSymlink, 10:
		m |= fs.ModeSymlink
	case typeChrdev, 12:
		m |= fs.ModeCharDevice | fs.ModeDevice
	case typeBlkdev, 11:
		m |= fs.ModeDevice
	case typeFifo, 13:
		m |= fs.ModeNamedPipe
	case typeSocket, 14:
		m |= fs.ModeSocket
	}
	return m
}
