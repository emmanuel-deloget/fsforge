package ext

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/alloc"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// variant captures what differs between the ext2/ext3/ext4 on-disk layouts.
type variant struct {
	useExtents   bool
	inodeSize    uint32
	defBlockSize uint32
	featIncompat uint32
	featRoCompat uint32
}

func ext2Variant() variant {
	return variant{
		inodeSize:    goodOldInodeSize,
		defBlockSize: defaultBlockSize,
		featIncompat: featIncompatFiletype,
		featRoCompat: featRoCompatSparseSuper,
	}
}

func ext4Variant() variant {
	return variant{
		useExtents:   true,
		inodeSize:    ext4InodeSize,
		defBlockSize: ext4DefaultBlockSize,
		featIncompat: featIncompatFiletype | featIncompatExtents,
		featRoCompat: featRoCompatSparseSuper,
	}
}

// Engine implements the ext2/ext3/ext4 family behind image.Filesystem. The
// variant selects the on-disk layout; everything else is shared.
type Engine struct {
	deps image.Deps
	v    variant
}

// NewExt2 returns an ext2 engine wired with deps. A nil allocator factory
// defaults to the deterministic bitmap allocator.
func NewExt2(deps image.Deps) *Engine { return newEngine(deps, ext2Variant()) }

// NewExt4 returns an ext4 engine (extents, 256-byte inodes) wired with deps.
func NewExt4(deps image.Deps) *Engine { return newEngine(deps, ext4Variant()) }

func newEngine(deps image.Deps, v variant) *Engine {
	if deps.Alloc == nil {
		deps.Alloc = alloc.BitmapFactory{}
	}
	return &Engine{deps: deps, v: v}
}

// ext2Image is an open image: a generic editable tree plus the device, geometry
// and engine needed to serialise it.
type ext2Image struct {
	*image.Mem
	dev    device.Device
	geo    geometry
	params image.Params
	deps   image.Deps
	eng    *Engine
}

// Format lays down a fresh, empty filesystem on dev.
func (e *Engine) Format(dev device.Device, p image.Params) (image.Image, error) {
	bs := p.BlockSize
	if bs == 0 {
		bs = e.v.defBlockSize
	}
	geo, err := computeGeometry(dev.Size(), bs, e.v.inodeSize)
	if err != nil {
		return nil, err
	}
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &ext2Image{Mem: mem, dev: dev, geo: geo, params: p, deps: e.deps, eng: e}, nil
}

var errTooManyInodes = errors.New("ext: not enough inodes for the tree")

// Finalize runs the deterministic layout pass: assign inode numbers, reserve
// metadata, allocate and write data, then bitmaps, inode tables, descriptors
// and superblocks.
func (img *ext2Image) Finalize() error {
	l := &layouter{
		dev:          img.dev,
		geo:          img.geo,
		deps:         img.deps,
		params:       img.params,
		useExtents:   img.eng.v.useExtents,
		featIncompat: img.eng.v.featIncompat,
		featRoCompat: img.eng.v.featRoCompat,
		al:           img.deps.Alloc.New(img.geo.totalBlocks),
		used:         make([]bool, img.geo.totalBlocks),
		inoOf:        make(map[*image.Node]uint32),
		inodes:       make(map[uint32]*inode),
		built:        make(map[*image.Node]bool),
		usedInos:     make(map[uint32]bool),
	}
	return l.run(img.RootNode())
}

type layouter struct {
	dev    device.Device
	geo    geometry
	deps   image.Deps
	params image.Params

	useExtents   bool
	featIncompat uint32
	featRoCompat uint32

	al       alloc.Allocator
	used     []bool
	inoOf    map[*image.Node]uint32
	inodes   map[uint32]*inode
	built    map[*image.Node]bool
	usedInos map[uint32]bool

	nextIno uint32
	curMeta uint64 // metadata (indirect) blocks of the inode being laid out
}

func (l *layouter) run(root *image.Node) error {
	l.ensureLostFound(root)

	// Pass A: assign inode numbers deterministically.
	l.inoOf[root] = rootIno
	l.usedInos[rootIno] = true
	if lf := findChild(root, "lost+found"); lf != nil {
		l.inoOf[lf.Node] = lostFoundIno
		l.usedInos[lostFoundIno] = true
	}
	for i := uint32(1); i <= reservedInos; i++ {
		l.usedInos[i] = true
	}
	l.nextIno = firstIno + 1 // 12
	if err := l.assignInodes(root); err != nil {
		return err
	}
	if max := l.nextIno - 1; uint64(max) > l.geo.inodesCount {
		return errTooManyInodes
	}

	// Reserve fixed metadata regions.
	if l.geo.firstDataBlock > 0 {
		l.reserve(0, l.geo.firstDataBlock) // boot block
	}
	for gr := uint64(0); gr < l.geo.numGroups; gr++ {
		_, _, _, firstData := l.geo.groupLayout(gr)
		l.reserve(l.geo.groupStart(gr), firstData-l.geo.groupStart(gr))
	}

	// Pass B: allocate and write data, building inode structures.
	if err := l.buildNode(root, rootIno); err != nil {
		return err
	}

	// Emit metadata.
	if err := l.writeInodeTables(); err != nil {
		return err
	}
	if err := l.writeBitmaps(); err != nil {
		return err
	}
	return l.writeDescriptorsAndSuperblocks()
}

// ensureLostFound creates a lost+found directory under root if absent, as
// e2fsck expects.
func (l *layouter) ensureLostFound(root *image.Node) {
	if findChild(root, "lost+found") != nil {
		return
	}
	meta := tree.Meta{Mode: fs.ModeDir | 0o700, ModTime: l.deps.Clock.Now()}
	n := &image.Node{Inode: tree.Inode{Meta: meta}, Nlink: 2}
	root.Children = append(root.Children, image.Entry{Name: "lost+found", Node: n})
	root.Nlink++
}

func (l *layouter) assignInodes(dir *image.Node) error {
	for _, e := range sortedChildren(dir) {
		if _, ok := l.inoOf[e.Node]; !ok {
			l.inoOf[e.Node] = l.nextIno
			l.usedInos[l.nextIno] = true
			l.nextIno++
		}
		if e.Node.IsDir() {
			if err := l.assignInodes(e.Node); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildNode lays out one node (and recurses into directories). parentIno is used
// to fill ".." for directories.
func (l *layouter) buildNode(n *image.Node, parentIno uint32) error {
	ino := l.inoOf[n]
	if l.built[n] {
		return nil // hard-linked node already laid out
	}
	l.built[n] = true

	switch {
	case n.IsDir():
		if err := l.buildDir(n, ino, parentIno); err != nil {
			return err
		}
		for _, e := range sortedChildren(n) {
			if err := l.buildNode(e.Node, ino); err != nil {
				return err
			}
		}
	case n.Mode&fs.ModeSymlink != 0:
		if err := l.buildSymlink(n, ino); err != nil {
			return err
		}
	case n.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
		l.buildSpecial(n, ino)
	default:
		if err := l.buildFile(n, ino); err != nil {
			return err
		}
	}
	return nil
}

func (l *layouter) buildDir(n *image.Node, ino, parentIno uint32) error {
	entries := []dentry{
		{ino: ino, name: ".", ftype: ftDir},
		{ino: parentIno, name: "..", ftype: ftDir},
	}
	for _, e := range sortedChildren(n) {
		entries = append(entries, dentry{
			ino:   l.inoOf[e.Node],
			name:  e.Name,
			ftype: dirFileType(e.Node.Mode),
		})
	}
	blocks := buildDirBlocks(entries, l.geo.blockSize)
	dataBlocks, err := l.allocContiguous(uint64(len(blocks)))
	if err != nil {
		return err
	}
	for i, b := range blocks {
		l.writeBlock(dataBlocks[i], b)
	}
	in := l.newInode(n, uint32(len(blocks))*l.geo.blockSize)
	if err := l.mapBlocks(in, dataBlocks); err != nil {
		return err
	}
	l.inodes[ino] = in
	return nil
}

func (l *layouter) buildFile(n *image.Node, ino uint32) error {
	var size uint64
	if n.Content != nil {
		size = uint64(n.Content.Size())
	}
	nblocks := ceilDiv(size, uint64(l.geo.blockSize))
	dataBlocks, err := l.allocContiguous(nblocks)
	if err != nil {
		return err
	}
	if err := l.writeContent(n.Content, dataBlocks, size); err != nil {
		return err
	}
	in := l.newInode(n, uint32(size))
	if err := l.mapBlocks(in, dataBlocks); err != nil {
		return err
	}
	l.inodes[ino] = in
	return nil
}

func (l *layouter) buildSymlink(n *image.Node, ino uint32) error {
	target := []byte(n.Link)
	in := l.newInode(n, uint32(len(target)))
	if len(target) < fastSymlinkMax {
		raw := make([]byte, totalIBlocks*4)
		copy(raw, target)
		in.blockRaw = raw // fast symlink, no data blocks
		l.inodes[ino] = in
		return nil
	}
	nblocks := ceilDiv(uint64(len(target)), uint64(l.geo.blockSize))
	dataBlocks, err := l.allocContiguous(nblocks)
	if err != nil {
		return err
	}
	if err := l.writeBytes(target, dataBlocks); err != nil {
		return err
	}
	if err := l.mapBlocks(in, dataBlocks); err != nil {
		return err
	}
	l.inodes[ino] = in
	return nil
}

func (l *layouter) buildSpecial(n *image.Node, ino uint32) {
	in := l.newInode(n, 0)
	if n.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0 {
		in.block[0] = uint32(n.Rdev) // old-style device number
	}
	l.inodes[ino] = in
}

// newInode builds the common inode fields. curMeta is reset so mapBlocks can
// accumulate indirect-block counts for i_blocks.
func (l *layouter) newInode(n *image.Node, size uint32) *inode {
	l.curMeta = 0
	t := uint32(n.ModTime.Unix())
	in := &inode{
		mode:       extMode(n.Mode),
		uid:        uint16(n.UID),
		gid:        uint16(n.GID),
		size:       size,
		atime:      t,
		ctime:      t,
		mtime:      t,
		linksCount: uint16(n.Nlink),
	}
	if l.geo.inodeSize > goodOldInodeSize {
		in.extra = extraISize
	}
	return in
}

func sectorsPerBlock(bs uint32) uint32 { return bs / 512 }

// mapBlocks records the data blocks in the inode. With extents (ext4) it writes
// an inline extent tree; otherwise it fills i_block with direct/indirect
// pointers. It then sets i_blocks (data + metadata, in 512-byte sectors).
func (l *layouter) mapBlocks(in *inode, data []uint64) error {
	if l.useExtents {
		raw, err := buildExtentsInline(data)
		if err != nil {
			return err
		}
		in.blockRaw = raw
		in.flags |= extentsFL
		in.blocks = uint32(uint64(len(data)) * uint64(sectorsPerBlock(l.geo.blockSize)))
		return nil
	}
	idx := 0
	for ; idx < directBlocks && idx < len(data); idx++ {
		in.block[idx] = uint32(data[idx])
	}
	if idx < len(data) {
		p, err := l.buildIndirect(1, data, &idx)
		if err != nil {
			return err
		}
		in.block[indSingle] = p
	}
	if idx < len(data) {
		p, err := l.buildIndirect(2, data, &idx)
		if err != nil {
			return err
		}
		in.block[indDouble] = p
	}
	if idx < len(data) {
		p, err := l.buildIndirect(3, data, &idx)
		if err != nil {
			return err
		}
		in.block[indTriple] = p
	}
	if idx < len(data) {
		return fmt.Errorf("ext: file exceeds maximum size (%d blocks)", len(data))
	}
	in.blocks = uint32((uint64(len(data)) + l.curMeta) * uint64(sectorsPerBlock(l.geo.blockSize)))
	return nil
}

// buildIndirect allocates one indirect block at the given level and fills it,
// consuming data blocks from data starting at *idx.
func (l *layouter) buildIndirect(level int, data []uint64, idx *int) (uint32, error) {
	blk, err := l.allocOne()
	if err != nil {
		return 0, err
	}
	l.curMeta++
	buf := make([]byte, l.geo.blockSize)
	ppb := int(l.geo.blockSize / 4)
	for i := 0; i < ppb && *idx < len(data); i++ {
		var ptr uint32
		if level == 1 {
			ptr = uint32(data[*idx])
			*idx++
		} else {
			ptr, err = l.buildIndirect(level-1, data, idx)
			if err != nil {
				return 0, err
			}
		}
		putU32(buf[i*4:], ptr)
	}
	l.writeBlock(blk, buf)
	return uint32(blk), nil
}
