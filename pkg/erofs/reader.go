package erofs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Open reads an existing EROFS image into the agnostic tree, so EROFS can be a
// conversion source. It understands both inode forms (compact 32-byte and
// extended 64-byte) and both uncompressed data layouts (FLAT_PLAIN and
// FLAT_INLINE), which covers fsforge's own output and a default mkfs.erofs
// image. Compressed inodes are rejected — fsforge does not ship an EROFS
// decompressor. File contents stay lazy; directory and symlink data are small
// and read eagerly. The returned image is read-only: rebuild via Convert.
func (e *Erofs) Open(dev device.Device) (image.Image, error) {
	hdr := make([]byte, superSize)
	if _, err := dev.ReadAt(hdr, superOffset); err != nil && err != io.EOF {
		return nil, err
	}
	sb, err := parseSuperblock(hdr)
	if err != nil {
		return nil, err
	}
	r := &ereader{dev: dev, sb: sb}
	root, err := r.readNode(uint64(sb.rootNid), uint64(sb.rootNid), map[uint64]*image.Node{})
	if err != nil {
		return nil, err
	}
	return &eImageRead{Mem: image.Adopt(e.deps, root)}, nil
}

// eImageRead is an opened (read-only) EROFS image. Rebuild via Convert to write
// changes; in-place re-finalize is not supported.
type eImageRead struct{ *image.Mem }

func (eImageRead) Finalize() error {
	return errors.New("erofs: cannot re-finalize an opened image; rebuild via convert")
}

type ereader struct {
	dev device.Device
	sb  superblock
}

// dinodeR is a parsed inode core plus the location of its data.
type dinodeR struct {
	mode  fs.FileMode
	size  int64
	union uint32
	nlink uint32
	uid   uint32
	gid   uint32
	mtime time.Time
	data  *erofsData
}

// readInode parses the inode at nid.
func (r *ereader) readInode(nid uint64) (dinodeR, error) {
	off := int64(r.sb.metaBlkaddr)*blockSize + int64(nid)*nidSlot
	b := make([]byte, inodeExtendedSize)
	if _, err := r.dev.ReadAt(b, off); err != nil && err != io.EOF {
		return dinodeR{}, err
	}

	format := le.Uint16(b[0:])
	version := format & 1
	layout := int((format >> 1) & 7)
	if layout != datalayoutFlatPlain && layout != datalayoutFlatInline {
		return dinodeR{}, fmt.Errorf("erofs: unsupported data layout %d at nid %d (compressed images cannot be read)", layout, nid)
	}
	icount := le.Uint16(b[2:])

	var in dinodeR
	var coreSize int64
	if version == inodeVersionExtended {
		coreSize = inodeExtendedSize
		in.mode = modeFromUnix(le.Uint16(b[4:]))
		in.size = int64(le.Uint64(b[8:]))
		in.union = le.Uint32(b[16:])
		in.uid = le.Uint32(b[24:])
		in.gid = le.Uint32(b[28:])
		in.mtime = time.Unix(int64(le.Uint64(b[32:])), int64(le.Uint32(b[40:]))).UTC()
		in.nlink = le.Uint32(b[44:])
	} else {
		coreSize = inodeCompactSize
		in.mode = modeFromUnix(le.Uint16(b[4:]))
		in.nlink = uint32(le.Uint16(b[6:]))
		in.size = int64(le.Uint32(b[8:]))
		in.union = le.Uint32(b[16:])
		in.uid = uint32(le.Uint16(b[24:]))
		in.gid = uint32(le.Uint16(b[26:]))
		in.mtime = time.Unix(int64(r.sb.buildTime), int64(r.sb.buildNsec)).UTC()
	}

	in.data = &erofsData{dev: r.dev, size: in.size}
	switch layout {
	case datalayoutFlatPlain:
		in.data.plainOff = int64(in.union) * blockSize
		in.data.fullBytes = in.size
	case datalayoutFlatInline:
		in.data.fullBytes = (in.size / blockSize) * blockSize
		in.data.plainOff = int64(in.union) * blockSize
		in.data.inlineOff = off + coreSize + xattrIbodySize(icount)
	}
	return in, nil
}

// readNode parses the inode at nid into a tree node, recursing into
// directories. seen shares one *Node across the nids of a hard-linked file.
func (r *ereader) readNode(nid, parentNid uint64, seen map[uint64]*image.Node) (*image.Node, error) {
	if n, ok := seen[nid]; ok {
		return n, nil
	}
	in, err := r.readInode(nid)
	if err != nil {
		return nil, err
	}
	n := &image.Node{Nlink: int(in.nlink)}
	n.Meta = tree.Meta{Mode: in.mode, UID: in.uid, GID: in.gid, ModTime: in.mtime}
	seen[nid] = n

	switch {
	case in.mode.IsDir():
		if err := r.readDir(n, in, nid, seen); err != nil {
			return nil, err
		}
	case in.mode&fs.ModeSymlink != 0:
		buf := make([]byte, in.size)
		if _, err := in.data.ReadAt(buf, 0); err != nil && err != io.EOF {
			return nil, err
		}
		n.Link = string(buf)
	case in.mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		n.Rdev = newDecodeDev(in.union)
	case in.mode&(fs.ModeNamedPipe|fs.ModeSocket) != 0:
		// no payload
	default:
		n.Content = in.data
	}
	return n, nil
}

// readDir parses a directory's blocks into dir's children, skipping "." and
// "..".
func (r *ereader) readDir(dir *image.Node, in dinodeR, nid uint64, seen map[uint64]*image.Node) error {
	data := make([]byte, in.size)
	if _, err := in.data.ReadAt(data, 0); err != nil && err != io.EOF {
		return err
	}

	for base := 0; base < len(data); base += blockSize {
		blockLen := blockSize
		if rem := len(data) - base; rem < blockLen {
			blockLen = rem
		}
		block := data[base : base+blockLen]
		if len(block) < direntSize {
			continue
		}
		nameoff0 := int(le.Uint16(block[8:]) & (blockSize - 1))
		if nameoff0 < direntSize || nameoff0 > blockLen {
			continue
		}
		ndir := nameoff0 / direntSize

		for k := 0; k < ndir; k++ {
			d := block[k*direntSize:]
			childNid := le.Uint64(d[0:])
			nameoff := int(le.Uint16(d[8:]) & (blockSize - 1))
			ftype := d[10]
			if nameoff < nameoff0 || nameoff > blockLen {
				continue
			}
			nameEnd := blockLen
			if k < ndir-1 {
				nameEnd = int(le.Uint16(block[(k+1)*direntSize+8:]) & (blockSize - 1))
			}
			if nameEnd > blockLen || nameEnd < nameoff {
				nameEnd = blockLen
			}
			name := trimNUL(block[nameoff:nameEnd])
			if name == "." || name == ".." || name == "" {
				continue
			}
			_ = ftype
			child, err := r.readNode(childNid, nid, seen)
			if err != nil {
				return err
			}
			dir.Children = append(dir.Children, image.Entry{Name: name, Node: child})
		}
	}
	return nil
}

// xattrIbodySize returns the size of the inline xattr body for an inode whose
// i_xattr_icount is icount (mirrors erofs_xattr_ibody_size).
func xattrIbodySize(icount uint16) int64 {
	if icount == 0 {
		return 0
	}
	return int64(12 + (int(icount)-1)*4)
}

func trimNUL(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// erofsData is a lazy tree.Source over an inode's data. FLAT_PLAIN serves all
// bytes from plainOff; FLAT_INLINE serves the leading whole blocks from plainOff
// and the trailing partial block from inlineOff.
type erofsData struct {
	dev       device.Device
	size      int64
	plainOff  int64
	inlineOff int64
	fullBytes int64
}

func (d *erofsData) Size() int64 { return d.size }

func (d *erofsData) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("erofs: negative offset")
	}
	if off >= d.size {
		return 0, io.EOF
	}
	want := int64(len(p))
	if rem := d.size - off; want > rem {
		want = rem
	}

	var n int
	for int64(n) < want {
		at := off + int64(n)
		var srcOff int64
		var chunk int64
		if at < d.fullBytes {
			srcOff = d.plainOff + at
			chunk = d.fullBytes - at
		} else {
			srcOff = d.inlineOff + (at - d.fullBytes)
			chunk = d.size - at
		}
		if rem := want - int64(n); chunk > rem {
			chunk = rem
		}
		m, err := d.dev.ReadAt(p[n:int64(n)+chunk], srcOff)
		n += m
		if err != nil && err != io.EOF {
			return n, err
		}
		if m == 0 {
			break
		}
	}
	if int64(n) < int64(len(p)) {
		return n, io.EOF
	}
	return n, nil
}
