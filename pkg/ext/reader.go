package ext

import (
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

var errBadMagic = errors.New("ext: bad superblock magic")

// reader parses an existing ext2 image into the agnostic node tree. File
// contents stay lazy through fileSource, honouring the "never buffer whole
// files" rule.
type reader struct {
	dev   device.Device
	sb    superblock
	bs    uint64
	ipg   uint64
	isize uint64
	first uint64
	descs []groupDesc
}

// Open loads an existing image for reading or offline mutation. The variant
// (ext2 vs ext4) is recovered from the superblock features so a re-Finalize
// preserves the on-disk format.
func (e *Engine) Open(dev device.Device) (image.Image, error) {
	r, err := newReader(dev)
	if err != nil {
		return nil, err
	}
	seen := make(map[uint32]*image.Node)
	root, err := r.readNode(rootIno, seen)
	if err != nil {
		return nil, err
	}

	geo, err := computeGeometry(dev.Size(), uint32(r.bs), uint32(r.isize))
	if err != nil {
		return nil, err
	}
	deps := e.deps
	deps.UUID = image.FixedUUID{V: r.sb.uuid} // preserve identity across re-finalize
	label := strings.TrimRight(string(r.sb.volumeName[:]), "\x00")

	v := variant{
		useExtents:   r.sb.featureIncompat&featIncompatExtents != 0,
		inodeSize:    uint32(r.isize),
		defBlockSize: uint32(r.bs),
		featIncompat: r.sb.featureIncompat,
		featRoCompat: r.sb.featureROCompat,
	}
	eng := &Engine{deps: deps, v: v}
	if eng.deps.Alloc == nil {
		eng.deps.Alloc = e.deps.Alloc
	}
	return &ext2Image{
		Mem:    image.Adopt(deps, root),
		dev:    dev,
		geo:    geo,
		params: image.Params{BlockSize: uint32(r.bs), Label: label},
		deps:   deps,
		eng:    eng,
		mutate: true,
	}, nil
}

func newReader(dev device.Device) (*reader, error) {
	raw := make([]byte, superblockSize)
	if _, err := dev.ReadAt(raw, superblockOffset); err != nil && err != io.EOF {
		return nil, err
	}
	sb := parseSuperblock(raw)
	if sb.magic != magic {
		return nil, errBadMagic
	}
	r := &reader{
		dev:   dev,
		sb:    sb,
		bs:    uint64(sb.blockSize()),
		ipg:   uint64(sb.inodesPerGroup),
		isize: uint64(sb.inodeSize),
		first: uint64(sb.firstDataBlock),
	}
	if r.isize == 0 {
		r.isize = goodOldInodeSize
	}
	numGroups := ceilDiv(uint64(sb.blocksCount)-r.first, uint64(sb.blocksPerGroup))
	gdtBlocks := ceilDiv(numGroups*descSize, r.bs)
	gdt := make([]byte, gdtBlocks*r.bs)
	if _, err := dev.ReadAt(gdt, int64(r.first+1)*int64(r.bs)); err != nil && err != io.EOF {
		return nil, err
	}
	r.descs = make([]groupDesc, numGroups)
	for i := range r.descs {
		r.descs[i] = parseGroupDesc(gdt[uint64(i)*descSize:])
	}
	return r, nil
}

func (r *reader) readBlock(block uint64) ([]byte, error) {
	buf := make([]byte, r.bs)
	_, err := r.dev.ReadAt(buf, int64(block)*int64(r.bs))
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

func (r *reader) readInode(ino uint32) (inode, error) {
	gr := (uint64(ino) - 1) / r.ipg
	idx := (uint64(ino) - 1) % r.ipg
	if int(gr) >= len(r.descs) {
		return inode{}, errors.New("ext: inode out of range")
	}
	off := int64(uint64(r.descs[gr].inodeTable)*r.bs + idx*r.isize)
	raw := make([]byte, r.isize)
	if _, err := r.dev.ReadAt(raw, off); err != nil && err != io.EOF {
		return inode{}, err
	}
	return parseInode(raw), nil
}

// blockList returns the ordered data block numbers backing an inode of the given
// size, from either the extent tree (ext4) or the indirect-block chain (ext2).
func (r *reader) blockList(in inode, size uint64) ([]uint64, error) {
	need := ceilDiv(size, r.bs)
	if in.flags&extentsFL != 0 {
		blocks, err := parseExtents(in.blockRaw, func(b uint64) ([]byte, error) { return r.readBlock(b) })
		if err != nil {
			return nil, err
		}
		if uint64(len(blocks)) > need {
			blocks = blocks[:need]
		}
		return blocks, nil
	}
	out := make([]uint64, 0, need)
	for i := 0; i < directBlocks && uint64(len(out)) < need; i++ {
		out = append(out, uint64(in.block[i]))
	}
	for _, lvl := range []struct {
		blk   uint32
		level int
	}{{in.block[indSingle], 1}, {in.block[indDouble], 2}, {in.block[indTriple], 3}} {
		if uint64(len(out)) >= need {
			break
		}
		if err := r.readIndirect(lvl.blk, lvl.level, need, &out); err != nil {
			return nil, err
		}
	}
	if uint64(len(out)) > need {
		out = out[:need]
	}
	return out, nil
}

func (r *reader) readIndirect(blk uint32, level int, need uint64, out *[]uint64) error {
	if blk == 0 {
		return nil
	}
	buf, err := r.readBlock(uint64(blk))
	if err != nil {
		return err
	}
	ppb := int(r.bs / 4)
	for i := 0; i < ppb && uint64(len(*out)) < need; i++ {
		ptr := binary.LittleEndian.Uint32(buf[i*4:])
		if level == 1 {
			*out = append(*out, uint64(ptr))
		} else if err := r.readIndirect(ptr, level-1, need, out); err != nil {
			return err
		}
	}
	return nil
}

// readNode builds the agnostic node for ino, sharing nodes across hard links.
func (r *reader) readNode(ino uint32, seen map[uint32]*image.Node) (*image.Node, error) {
	if n, ok := seen[ino]; ok {
		return n, nil
	}
	in, err := r.readInode(ino)
	if err != nil {
		return nil, err
	}
	mode := goMode(in.mode)
	t := time.Unix(int64(in.mtime), 0).UTC()
	n := &image.Node{
		Inode: tree.Inode{Meta: tree.Meta{
			Mode:    mode,
			UID:     uint32(in.uid),
			GID:     uint32(in.gid),
			ModTime: t,
		}},
		Nlink: int(in.linksCount),
	}
	seen[ino] = n

	switch {
	case mode&fs.ModeDir != 0:
		if err := r.readDir(&in, n, seen); err != nil {
			return nil, err
		}
	case mode&fs.ModeSymlink != 0:
		target, err := r.readSymlink(&in)
		if err != nil {
			return nil, err
		}
		n.Link = target
	case mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		n.Rdev = uint64(in.block[0])
	case mode&(fs.ModeNamedPipe|fs.ModeSocket) != 0:
		// no payload
	default:
		blocks, err := r.blockList(in, uint64(in.size))
		if err != nil {
			return nil, err
		}
		n.Content = &fileSource{dev: r.dev, blocks: blocks, size: int64(in.size), bs: int64(r.bs)}
	}
	return n, nil
}

func (r *reader) readDir(in *inode, n *image.Node, seen map[uint32]*image.Node) error {
	blocks, err := r.blockList(*in, uint64(in.size))
	if err != nil {
		return err
	}
	for _, blk := range blocks {
		buf, err := r.readBlock(blk)
		if err != nil {
			return err
		}
		var perr error
		parseDirBlock(buf, func(ino uint32, name string, _ byte) {
			if perr != nil || name == "." || name == ".." {
				return
			}
			child, e := r.readNode(ino, seen)
			if e != nil {
				perr = e
				return
			}
			n.Children = append(n.Children, image.Entry{Name: name, Node: child})
		})
		if perr != nil {
			return perr
		}
	}
	return nil
}

func (r *reader) readSymlink(in *inode) (string, error) {
	if in.blocks == 0 { // fast symlink stored in the i_block area
		raw := in.blockRaw
		if raw == nil {
			raw = make([]byte, totalIBlocks*4)
			for i := 0; i < totalIBlocks; i++ {
				binary.LittleEndian.PutUint32(raw[i*4:], in.block[i])
			}
		}
		return strings.TrimRight(string(raw[:min(len(raw), int(in.size))]), "\x00"), nil
	}
	blocks, err := r.blockList(*in, uint64(in.size))
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	left := int(in.size)
	for _, blk := range blocks {
		buf, err := r.readBlock(blk)
		if err != nil {
			return "", err
		}
		n := min(left, len(buf))
		sb.Write(buf[:n])
		left -= n
	}
	return sb.String(), nil
}

// fileSource reads a regular file's contents lazily through its block list.
type fileSource struct {
	dev    device.Device
	blocks []uint64
	size   int64
	bs     int64
}

func (f *fileSource) Size() int64 { return f.size }

func (f *fileSource) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("ext: negative offset")
	}
	if off >= f.size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && off < f.size {
		idx := off / f.bs
		within := off % f.bs
		blk := f.blocks[idx]
		buf := make([]byte, f.bs)
		if _, err := f.dev.ReadAt(buf, int64(blk)*f.bs); err != nil && err != io.EOF {
			return n, err
		}
		avail := f.bs - within
		if rem := f.size - off; rem < avail {
			avail = rem
		}
		c := copy(p[n:], buf[within:within+avail])
		n += c
		off += int64(c)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func goMode(m uint16) fs.FileMode {
	perm := fs.FileMode(m & 0o777)
	if m&modeSetuid != 0 {
		perm |= fs.ModeSetuid
	}
	if m&modeSetgid != 0 {
		perm |= fs.ModeSetgid
	}
	if m&modeSticky != 0 {
		perm |= fs.ModeSticky
	}
	switch m & 0xF000 {
	case modeDir:
		perm |= fs.ModeDir
	case modeSymlink:
		perm |= fs.ModeSymlink
	case modeChrdev:
		perm |= fs.ModeCharDevice
	case modeBlkdev:
		perm |= fs.ModeDevice
	case modeFifo:
		perm |= fs.ModeNamedPipe
	case modeSock:
		perm |= fs.ModeSocket
	}
	return perm
}
