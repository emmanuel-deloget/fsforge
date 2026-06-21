package cramfs

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Open reads an existing cramfs image into the agnostic tree, so cramfs can be a
// conversion source. Directory entries are read directly; regular-file and
// symlink data is zlib-decompressed per 4 KiB block. The returned image is
// read-only; rebuild via Convert to change it.
func (e *Cramfs) Open(dev device.Device) (image.Image, error) {
	sb := make([]byte, superblockSize)
	if _, err := dev.ReadAt(sb, 0); err != nil && err != io.EOF {
		return nil, err
	}
	if le.Uint32(sb[0:]) != magic {
		return nil, errors.New("cramfs: bad magic")
	}
	r := &creader{dev: dev}
	rootIn := parseInode(sb[rootInodeOffset:])
	root := &image.Node{Nlink: 2}
	root.Meta = tree.Meta{Mode: modeFromUnix(rootIn.mode) | fs.ModeDir}
	if rootIn.size > 0 {
		if err := r.readDir(root, rootIn.offset<<2, rootIn.size); err != nil {
			return nil, err
		}
	}
	return &cImageRead{Mem: image.Adopt(e.deps, root)}, nil
}

type cImageRead struct{ *image.Mem }

func (cImageRead) Finalize() error {
	return errors.New("cramfs: cannot re-finalize an opened image; rebuild via convert")
}

type creader struct{ dev device.Device }

// readDir parses a directory's entries region (inode + padded name pairs) into
// dir's children, recursing into subdirectories.
func (r *creader) readDir(dir *image.Node, off, size uint32) error {
	data := make([]byte, size)
	if _, err := r.dev.ReadAt(data, int64(off)); err != nil && err != io.EOF {
		return err
	}
	for pos := uint32(0); pos+inodeSize <= size; {
		in := parseInode(data[pos:])
		nameLen := in.namelen * 4
		nameStart := pos + inodeSize
		if nameStart+nameLen > size {
			break
		}
		name := trimNUL(data[nameStart : nameStart+nameLen])
		pos = nameStart + nameLen

		mode := modeFromUnix(in.mode)
		n := &image.Node{Nlink: 1}
		n.Meta = tree.Meta{Mode: mode, UID: in.uid, GID: in.gid}
		switch {
		case mode.IsDir():
			n.Nlink = 2
			if in.size > 0 {
				if err := r.readDir(n, in.offset<<2, in.size); err != nil {
					return err
				}
			}
		case mode&fs.ModeSymlink != 0:
			target, err := r.readData(in.offset<<2, int64(in.size))
			if err != nil {
				return err
			}
			n.Link = string(target)
		case mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
			n.Rdev = uint64(in.size)
		case mode&(fs.ModeNamedPipe|fs.ModeSocket) != 0:
			// no payload
		default:
			if in.size > 0 {
				n.Content = &cramfsFile{r: r, dataOff: in.offset << 2, size: int64(in.size)}
			} else {
				n.Content = tree.Bytes(nil)
			}
		}
		dir.Children = append(dir.Children, image.Entry{Name: name, Node: n})
	}
	return nil
}

// readData decompresses all of a file's or symlink's blocks into memory.
func (r *creader) readData(dataOff uint32, size int64) ([]byte, error) {
	out := make([]byte, 0, size)
	nblocks := int((size + blockSize - 1) / blockSize)
	for i := 0; i < nblocks; i++ {
		b, err := r.block(dataOff, nblocks, i)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	if int64(len(out)) > size {
		out = out[:size]
	}
	return out, nil
}

// block reads and decompresses block i of a file whose data region starts at
// dataOff with nblocks blocks.
func (r *creader) block(dataOff uint32, nblocks, i int) ([]byte, error) {
	ptrs := make([]byte, nblocks*4)
	if _, err := r.dev.ReadAt(ptrs, int64(dataOff)); err != nil && err != io.EOF {
		return nil, err
	}
	start := dataOff + uint32(nblocks*4)
	if i > 0 {
		start = le.Uint32(ptrs[(i-1)*4:]) &^ blkUncompressed
	}
	raw := le.Uint32(ptrs[i*4:])
	end := raw &^ blkUncompressed
	comp := make([]byte, end-start)
	if _, err := r.dev.ReadAt(comp, int64(start)); err != nil && err != io.EOF {
		return nil, err
	}
	if raw&blkUncompressed != 0 {
		return comp, nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(comp))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// cramfsFile is a lazy tree.Source decompressing a regular file's blocks; it
// caches the most recently read block for sequential reads.
type cramfsFile struct {
	r       *creader
	dataOff uint32
	size    int64
	cacheIx int
	cache   []byte
}

func (f *cramfsFile) Size() int64 { return f.size }

func (f *cramfsFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("cramfs: negative offset")
	}
	if off >= f.size {
		return 0, io.EOF
	}
	nblocks := int((f.size + blockSize - 1) / blockSize)
	want := int64(len(p))
	if rem := f.size - off; want > rem {
		want = rem
	}
	var n int64
	for n < want {
		ix := int((off + n) / blockSize)
		blk, err := f.readBlock(ix, nblocks)
		if err != nil {
			return int(n), err
		}
		within := (off + n) % blockSize
		m := copy(p[n:want], blk[within:])
		n += int64(m)
	}
	if n < int64(len(p)) {
		return int(n), io.EOF
	}
	return int(n), nil
}

func (f *cramfsFile) readBlock(ix, nblocks int) ([]byte, error) {
	if f.cache != nil && f.cacheIx == ix {
		return f.cache, nil
	}
	b, err := f.r.block(f.dataOff, nblocks, ix)
	if err != nil {
		return nil, err
	}
	f.cache, f.cacheIx = b, ix
	return b, nil
}

func trimNUL(b []byte) string {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0 {
			return string(b[:i+1])
		}
	}
	return ""
}
