package iso

import (
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

const (
	sectorSize    = 2048
	pvdSector     = 16
	systemAreaSec = 16
	maxDepth      = 8
	maxRecordLen  = 255
)

var le = binary.LittleEndian

// ISO is the ISO9660 + Rock Ridge create engine, implementing image.Filesystem.
// Rock Ridge carries POSIX names, permissions, symlinks and device nodes that
// plain ISO9660 cannot; images are validated by xorriso. Because ISO9660 is a
// read-only on-disk format, Open is not supported — rebuild to change an image.
//
// Format sizes the volume from the tree, so the backing device should be at
// least as large as the content plus ISO overhead; the CLI trims the file to
// the recorded volume size afterwards.
type ISO struct{ deps image.Deps }

// New returns an ISO engine wired with deps. A nil Clock is replaced with the
// host system clock.
func New(deps image.Deps) *ISO {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	return &ISO{deps: deps}
}

type isoImage struct {
	*image.Mem
	dev   device.Device
	label string
	deps  image.Deps
}

// Format starts a fresh ISO image on dev.
func (e *ISO) Format(dev device.Device, p image.Params) (image.Image, error) {
	label := p.Label
	if label == "" {
		label = "FSFORGE"
	}
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &isoImage{Mem: mem, dev: dev, label: label, deps: e.deps}, nil
}

// Open is not supported: ISO9660 is read-only on disk; rebuild instead.
func (e *ISO) Open(device.Device) (image.Image, error) {
	return nil, errors.New("iso: Open not supported; rebuild instead")
}

// dirNode is the layout bookkeeping for one directory.
type dirNode struct {
	node     *image.Node
	parent   *dirNode
	name     string // this directory's own name ("" for root)
	number   int    // path-table directory number (root = 1)
	extent   uint32 // first sector of the directory's records
	sectors  uint32 // size in sectors
	depth    int
	children []*image.Entry // sorted child entries
}

type layouter struct {
	dev   device.Device
	deps  image.Deps
	label string

	dirs     []*dirNode
	fileExt  map[*image.Node]uint32 // file node -> extent LBA
	fileSecs map[*image.Node]uint32
	next     uint32 // next free sector
}

// Finalize writes the ISO image.
func (img *isoImage) Finalize() error {
	l := &layouter{
		dev:      img.dev,
		deps:     img.deps,
		label:    img.label,
		fileExt:  map[*image.Node]uint32{},
		fileSecs: map[*image.Node]uint32{},
	}
	return l.run(img.RootNode())
}

func (l *layouter) run(root *image.Node) error {
	// Collect directories breadth-first so path-table numbers are ordered.
	rootDir := &dirNode{node: root, number: 1, depth: 1}
	rootDir.parent = rootDir // ".." of root points at root
	l.dirs = []*dirNode{rootDir}
	for i := 0; i < len(l.dirs); i++ {
		d := l.dirs[i]
		if d.depth > maxDepth {
			return errors.New("iso: directory depth exceeds 8 (deep relocation unsupported)")
		}
		for _, e := range sortedChildren(d.node) {
			ent := e
			d.children = append(d.children, &ent)
			if e.Node.IsDir() {
				child := &dirNode{node: e.Node, parent: d, name: e.Name, depth: d.depth + 1}
				l.dirs = append(l.dirs, child)
			}
		}
	}
	for i, d := range l.dirs {
		d.number = i + 1
	}

	// Size each directory's records.
	for _, d := range l.dirs {
		n, err := l.dirSectors(d)
		if err != nil {
			return err
		}
		d.sectors = n
	}

	// Path table size (identical for L and M).
	ptSize := l.pathTableSize()
	ptSectors := sectors(uint64(ptSize))

	// Assign LBAs: terminator at 17, then L and M path tables, dirs, files.
	l.next = pvdSector + 2 // 18
	lPathLBA := l.next
	l.next += ptSectors
	mPathLBA := l.next
	l.next += ptSectors
	for _, d := range l.dirs {
		d.extent = l.next
		l.next += d.sectors
	}
	for _, d := range l.dirs {
		for _, e := range d.children {
			if !e.Node.IsDir() {
				l.assignFile(e.Node)
			}
		}
	}
	totalSectors := l.next

	// Write everything.
	for _, d := range l.dirs {
		if err := l.writeDir(d); err != nil {
			return err
		}
	}
	if err := l.writeFiles(); err != nil {
		return err
	}
	l.writePathTable(lPathLBA, false)
	l.writePathTable(mPathLBA, true)
	l.writePVD(totalSectors, ptSize, lPathLBA, mPathLBA, l.dirs[0])
	l.writeTerminator()
	return nil
}

func (l *layouter) assignFile(n *image.Node) {
	if _, ok := l.fileExt[n]; ok {
		return
	}
	var size uint64
	if n.Content != nil {
		size = uint64(n.Content.Size())
	}
	secs := sectors(size)
	l.fileExt[n] = l.next
	l.fileSecs[n] = secs
	l.next += secs
}

// --- sizing ---

func (l *layouter) dirSectors(d *dirNode) (uint32, error) {
	// "." and ".." plus each child; records may not cross a sector boundary.
	used := 0
	add := func(recLen int) {
		if used%sectorSize+recLen > sectorSize {
			used += sectorSize - used%sectorSize // pad to next sector
		}
		used += recLen
	}
	isRoot := d == l.dirs[0]
	dotLen, err := l.recordLen(d.node, ".", isRoot)
	if err != nil {
		return 0, err
	}
	add(dotLen)
	dotdotLen, err := l.recordLen(d.node, "..", false)
	if err != nil {
		return 0, err
	}
	add(dotdotLen)
	for _, e := range d.children {
		rl, err := l.recordLen(e.Node, e.Name, false)
		if err != nil {
			return 0, err
		}
		add(rl)
	}
	return sectors(uint64(used)), nil
}

// recordLen computes a directory record length including Rock Ridge SUA.
func (l *layouter) recordLen(n *image.Node, name string, isRootDot bool) (int, error) {
	base := recordBase(n, name)
	total := base + l.suaLen(n, name, isRootDot)
	if total > maxRecordLen {
		return 0, errors.New("iso: directory record too long (name/symlink overflows; CE unsupported): " + name)
	}
	return total, nil
}

// recordBase is the fixed part of a directory record (header + identifier,
// padded to even length).
func recordBase(n *image.Node, name string) int {
	base := 33 + len(isoIdentifier(n, name))
	if base%2 == 1 {
		base++
	}
	return base
}

// --- writing ---

func (l *layouter) writeDir(d *dirNode) error {
	buf := make([]byte, d.sectors*sectorSize)
	off := 0
	emit := func(rec []byte) {
		if off%sectorSize+len(rec) > sectorSize {
			off += sectorSize - off%sectorSize
		}
		copy(buf[off:], rec)
		off += len(rec)
	}
	emit(l.dirRecord(d.node, ".", d.extent, d.sectors*sectorSize, true, d == l.dirs[0]))
	emit(l.dirRecord(d.parent.node, "..", d.parent.extent, d.parent.sectors*sectorSize, true, false))
	for _, e := range d.children {
		if e.Node.IsDir() {
			cd := l.findDir(e.Node)
			emit(l.dirRecord(e.Node, e.Name, cd.extent, cd.sectors*sectorSize, true, false))
		} else {
			ext := l.fileExt[e.Node]
			var size uint32
			if e.Node.Content != nil {
				size = uint32(e.Node.Content.Size())
			}
			emit(l.dirRecord(e.Node, e.Name, ext, size, false, false))
		}
	}
	_, err := l.dev.WriteAt(buf, int64(d.extent)*sectorSize)
	return err
}

func (l *layouter) writeFiles() error {
	for _, d := range l.dirs {
		for _, e := range d.children {
			if e.Node.IsDir() || e.Node.Content == nil {
				continue
			}
			ext := l.fileExt[e.Node]
			size := e.Node.Content.Size()
			buf := make([]byte, l.fileSecs[e.Node]*sectorSize)
			if _, err := e.Node.Content.ReadAt(buf[:size], 0); err != nil && err != io.EOF {
				return err
			}
			if _, err := l.dev.WriteAt(buf, int64(ext)*sectorSize); err != nil {
				return err
			}
		}
	}
	return nil
}

// dirRecord builds a directory record for name pointing at extent/size.
func (l *layouter) dirRecord(n *image.Node, name string, extent, dataLen uint32, isDir, isRootDot bool) []byte {
	id := isoIdentifier(n, name)
	base := recordBase(n, name)
	sua := l.suaLen(n, name, isRootDot)
	rec := make([]byte, base+sua)
	rec[0] = byte(base + sua)
	putBoth32(rec[2:], extent)
	putBoth32(rec[10:], dataLen)
	putDirTime(rec[18:], nodeTime(n, l.deps))
	if isDir {
		rec[25] = 0x02 // directory flag
	}
	putBoth16(rec[28:], 1) // volume sequence number
	rec[32] = byte(len(id))
	copy(rec[33:], id)
	l.writeSUA(rec[base:], n, name, isRootDot)
	return rec
}

func (l *layouter) writePathTable(lba uint32, bigEndian bool) {
	buf := make([]byte, 0, 256)
	for _, d := range l.dirs {
		id := pathTableID(d)
		rec := make([]byte, 8+len(id))
		rec[0] = byte(len(id))
		if bigEndian {
			be32(rec[2:], d.extent)
			be16(rec[6:], uint16(d.parent.number))
		} else {
			le.PutUint32(rec[2:], d.extent)
			le.PutUint16(rec[6:], uint16(d.parent.number))
		}
		copy(rec[8:], id)
		if len(rec)%2 == 1 {
			rec = append(rec, 0)
		}
		buf = append(buf, rec...)
	}
	out := make([]byte, sectors(uint64(len(buf)))*sectorSize)
	copy(out, buf)
	l.dev.WriteAt(out, int64(lba)*sectorSize)
}

func (l *layouter) pathTableSize() uint32 {
	size := 0
	for _, d := range l.dirs {
		idLen := len(pathTableID(d))
		rec := 8 + idLen
		if rec%2 == 1 {
			rec++
		}
		size += rec
	}
	return uint32(size)
}

func (l *layouter) writePVD(totalSectors, ptSize, lPath, mPath uint32, root *dirNode) {
	b := make([]byte, sectorSize)
	b[0] = 1
	copy(b[1:6], "CD001")
	b[6] = 1
	padField(b[8:40], strings.ToUpper("FSFORGE"))
	padField(b[40:72], strings.ToUpper(l.label))
	putBoth32(b[80:], totalSectors)
	putBoth16(b[120:], 1) // volume set size
	putBoth16(b[124:], 1) // volume sequence number
	putBoth16(b[128:], sectorSize)
	putBoth32(b[132:], ptSize)
	le.PutUint32(b[140:], lPath)
	be32(b[148:], mPath)
	// The PVD root directory record is exactly 34 bytes with no system-use area
	// (the Rock Ridge entries live in the root directory's own "." record).
	copy(b[156:190], l.rootRecord34(root))
	for _, off := range []int{190, 318, 446, 574} { // set/publisher/preparer/app ids
		spaces(b[off : off+128])
	}
	spaces(b[702:739])
	spaces(b[739:776])
	spaces(b[776:813])
	t := volTime(nodeTime(root.node, l.deps))
	copy(b[813:830], t)
	copy(b[830:847], t)
	copy(b[847:864], zeroVolTime())
	copy(b[864:881], zeroVolTime())
	b[881] = 1 // file structure version
	l.dev.WriteAt(b, pvdSector*sectorSize)
}

// rootRecord34 builds the fixed 34-byte root directory record for the PVD.
func (l *layouter) rootRecord34(root *dirNode) []byte {
	rec := make([]byte, 34)
	rec[0] = 34
	putBoth32(rec[2:], root.extent)
	putBoth32(rec[10:], root.sectors*sectorSize)
	putDirTime(rec[18:], nodeTime(root.node, l.deps))
	rec[25] = 0x02 // directory
	putBoth16(rec[28:], 1)
	rec[32] = 1 // identifier length
	rec[33] = 0 // "." special identifier
	return rec
}

func (l *layouter) writeTerminator() {
	b := make([]byte, sectorSize)
	b[0] = 255
	copy(b[1:6], "CD001")
	b[6] = 1
	l.dev.WriteAt(b, (pvdSector+1)*sectorSize)
}

func (l *layouter) findDir(n *image.Node) *dirNode {
	for _, d := range l.dirs {
		if d.node == n {
			return d
		}
	}
	return nil
}

// --- helpers ---

func sectors(n uint64) uint32 { return uint32((n + sectorSize - 1) / sectorSize) }

func sortedChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func putBoth32(b []byte, v uint32) {
	le.PutUint32(b[0:], v)
	be32(b[4:], v)
}

func putBoth16(b []byte, v uint16) {
	le.PutUint16(b[0:], v)
	be16(b[4:], v)
}

func be32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
}

func be16(b []byte, v uint16) { b[0], b[1] = byte(v>>8), byte(v) }

func spaces(b []byte) {
	for i := range b {
		b[i] = ' '
	}
}

func padField(b []byte, s string) {
	spaces(b)
	copy(b, s)
}
