package romfs

import (
	"errors"
	"io"
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Open reads an existing romfs image into the agnostic tree, so romfs can be a
// conversion source. It parses the superblock, locates the root inode and walks
// each directory's linked list of file headers. Images written by genromfs open
// too. The returned image is read-only; rebuild via Convert to change it.
func (e *Romfs) Open(dev device.Device) (image.Image, error) {
	head := make([]byte, headerSize)
	if _, err := dev.ReadAt(head, 0); err != nil && err != io.EOF {
		return nil, err
	}
	if be.Uint32(head[0:]) != magicW0 || be.Uint32(head[4:]) != magicW1 {
		return nil, errors.New("romfs: bad magic")
	}
	r := &rreader{dev: dev}

	vol, err := r.name(headerSize)
	if err != nil {
		return nil, err
	}
	pos := alignUp(headerSize + uint32(len(vol)) + 1)

	rootHdr, err := r.header(pos)
	if err != nil {
		return nil, err
	}
	root := &image.Node{Nlink: 2}
	root.Meta = tree.Meta{Mode: fs.ModeDir | 0o755}
	if err := r.readDir(root, rootHdr.spec&alignMask, map[uint32]bool{}); err != nil {
		return nil, err
	}
	return &rImageRead{Mem: image.Adopt(e.deps, root)}, nil
}

type rImageRead struct{ *image.Mem }

func (rImageRead) Finalize() error {
	return errors.New("romfs: cannot re-finalize an opened image; rebuild via convert")
}

type rreader struct{ dev device.Device }

type rhdr struct {
	next  uint32 // includes the low flag bits
	spec  uint32
	size  uint32
	off   uint32
	dtype uint32
	exec  bool
}

func (r *rreader) header(off uint32) (rhdr, error) {
	b := make([]byte, headerSize)
	if _, err := r.dev.ReadAt(b, int64(off)); err != nil && err != io.EOF {
		return rhdr{}, err
	}
	next := be.Uint32(b[0:])
	return rhdr{
		next:  next,
		spec:  be.Uint32(b[4:]),
		size:  be.Uint32(b[8:]),
		off:   off,
		dtype: next & typeMask,
		exec:  next&flagExec != 0,
	}, nil
}

// name reads a NUL-terminated, 16-byte-padded name starting at off.
func (r *rreader) name(off uint32) (string, error) {
	b := make([]byte, 256)
	if _, err := r.dev.ReadAt(b, int64(off)); err != nil && err != io.EOF {
		return "", err
	}
	for i, c := range b {
		if c == 0 {
			return string(b[:i]), nil
		}
	}
	return string(b), nil
}

// readDir walks the linked list of file headers starting at off into dir's
// children, skipping the "." and ".." entries and recursing into directories.
func (r *rreader) readDir(dir *image.Node, off uint32, seen map[uint32]bool) error {
	for off != 0 {
		if seen[off] {
			break // guard against a malformed cycle
		}
		seen[off] = true

		h, err := r.header(off)
		if err != nil {
			return err
		}
		name, err := r.name(off + headerSize)
		if err != nil {
			return err
		}
		if name != "." && name != ".." && name != "" {
			child, err := r.node(h, name)
			if err != nil {
				return err
			}
			dir.Children = append(dir.Children, image.Entry{Name: name, Node: child})
		}
		off = h.next & alignMask
	}
	return nil
}

// node builds a tree node from a header, following hard links and recursing into
// directories.
func (r *rreader) node(h rhdr, name string) (*image.Node, error) {
	if h.dtype == typeHardlink {
		// A real hard link: resolve to the target inode and read that instead.
		target, err := r.header(h.spec & alignMask)
		if err != nil {
			return nil, err
		}
		return r.node(target, name)
	}

	n := &image.Node{Nlink: 1, Inode: tree.Inode{Meta: tree.Meta{Mode: modeOf(h.dtype, h.exec)}}}
	dataOff := h.off + headerSize + paddedName(name)
	switch h.dtype {
	case typeDir:
		n.Nlink = 2
		if err := r.readDir(n, h.spec&alignMask, map[uint32]bool{}); err != nil {
			return nil, err
		}
	case typeSymlink:
		target := make([]byte, h.size)
		if _, err := r.dev.ReadAt(target, int64(dataOff)); err != nil && err != io.EOF {
			return nil, err
		}
		n.Link = string(target)
	case typeChar, typeBlock:
		n.Rdev = uint64(h.spec>>16)<<8 | uint64(h.spec&0xffff)
	case typeFifo, typeSocket:
		// no payload
	default: // regular file
		if h.size > 0 {
			n.Content = &romfsFile{dev: r.dev, off: int64(dataOff), size: int64(h.size)}
		} else {
			n.Content = tree.Bytes(nil)
		}
	}
	return n, nil
}

// romfsFile is a lazy tree.Source over a regular file's contiguous data.
type romfsFile struct {
	dev  device.Device
	off  int64
	size int64
}

func (f *romfsFile) Size() int64 { return f.size }

func (f *romfsFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("romfs: negative offset")
	}
	if off >= f.size {
		return 0, io.EOF
	}
	if rem := f.size - off; int64(len(p)) > rem {
		n, err := f.dev.ReadAt(p[:rem], f.off+off)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return f.dev.ReadAt(p, f.off+off)
}
