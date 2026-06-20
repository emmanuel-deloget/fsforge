package fat

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

var le = binary.LittleEndian

// FAT is the FAT32 engine.
type FAT struct{ deps image.Deps }

// New returns a FAT32 engine wired with deps.
func New(deps image.Deps) *FAT {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	if deps.UUID == nil {
		deps.UUID = image.RandomUUID{}
	}
	return &FAT{deps: deps}
}

type fatImage struct {
	*image.Mem
	dev   device.Device
	geo   geometry
	label string
	deps  image.Deps
}

// Format prepares a fresh FAT32 volume on dev.
func (e *FAT) Format(dev device.Device, p image.Params) (image.Image, error) {
	geo, err := computeGeometry(dev.Size())
	if err != nil {
		return nil, err
	}
	label := p.Label
	if label == "" {
		label = "NO NAME"
	}
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &fatImage{Mem: mem, dev: dev, geo: geo, label: label, deps: e.deps}, nil
}

// Open is not supported yet; FAT mutation will rebuild like squashfs.
func (e *FAT) Open(device.Device) (image.Image, error) {
	return nil, errors.New("fat: Open not supported (create-only for now)")
}

type layouter struct {
	dev   device.Device
	geo   geometry
	deps  image.Deps
	label string

	fat          []uint32 // FAT entries, indexed by cluster number
	nextFree     uint32
	usedClusters uint64
}

// Finalize writes the FAT32 volume deterministically.
func (img *fatImage) Finalize() error {
	g := img.geo
	l := &layouter{
		dev:      img.dev,
		geo:      g,
		deps:     img.deps,
		label:    img.label,
		fat:      make([]uint32, g.clusters+2),
		nextFree: rootCluster,
	}
	l.fat[0] = 0x0FFFFFF8
	l.fat[1] = 0x0FFFFFFF

	rootClus, err := l.layoutDir(img.RootNode(), 0, true)
	if err != nil {
		return err
	}
	if rootClus != rootCluster {
		return fmt.Errorf("fat: root cluster = %d, want %d", rootClus, rootCluster)
	}
	if err := l.writeFATs(); err != nil {
		return err
	}
	return l.writeBootAndInfo()
}

// allocChain reserves n contiguous-in-number clusters (numbering is sequential,
// so chains are naturally contiguous and deterministic) and links them in the
// FAT. Returns the first cluster, or 0 for n==0.
func (l *layouter) allocChain(n uint64) (uint32, error) {
	if n == 0 {
		return 0, nil
	}
	if uint64(l.nextFree)-2+n > l.geo.clusters {
		return 0, fmt.Errorf("fat: out of clusters (need %d)", n)
	}
	first := l.nextFree
	for i := uint64(0); i < n; i++ {
		c := first + uint32(i)
		if i+1 < n {
			l.fat[c] = c + 1
		} else {
			l.fat[c] = fatEOC
		}
	}
	l.nextFree += uint32(n)
	l.usedClusters += n
	return first, nil
}

type childInfo struct {
	node      *image.Node
	name      string
	short     string
	firstClus uint32
	size      uint32
	attr      byte
}

func (l *layouter) layoutDir(n *image.Node, parentClus uint32, isRoot bool) (uint32, error) {
	namer := newShortNamer()
	children := sortedChildren(n)

	// Count entries to size the directory before allocating it (entry count
	// depends only on names, not on child clusters).
	infos := make([]childInfo, 0, len(children))
	entryCount := 0
	if !isRoot {
		entryCount += 2 // "." and ".."
	} else {
		entryCount++ // volume label entry
	}
	for _, e := range children {
		short := namer.generate(e.Name)
		var attr byte = attrArchive
		if e.Node.IsDir() {
			attr = attrDirectory
		}
		infos = append(infos, childInfo{node: e.Node, name: e.Name, short: short, attr: attr})
		entryCount += lfnCount(e.Name) + 1
	}

	cs := l.geo.clusterSizeBytes()
	entriesPerClus := cs / dirEntrySize
	nClus := (uint64(entryCount) + uint64(entriesPerClus) - 1) / uint64(entriesPerClus)
	if nClus == 0 {
		nClus = 1
	}
	dirClus, err := l.allocChain(nClus)
	if err != nil {
		return 0, err
	}

	// Lay out children now that the directory's own cluster is known.
	for i := range infos {
		ci := &infos[i]
		switch {
		case ci.node.Mode&fs.ModeSymlink != 0:
			return 0, fmt.Errorf("fat: symlink %q not representable in FAT", ci.name)
		case ci.node.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
			return 0, fmt.Errorf("fat: special file %q not representable in FAT", ci.name)
		case ci.node.IsDir():
			fc, err := l.layoutDir(ci.node, dirClus, false)
			if err != nil {
				return 0, err
			}
			ci.firstClus = fc
		default:
			fc, sz, err := l.layoutFile(ci.node)
			if err != nil {
				return 0, err
			}
			ci.firstClus, ci.size = fc, sz
		}
	}

	// Build and write the directory's entries.
	buf := make([]byte, nClus*uint64(cs))
	off := 0
	if isRoot {
		off += l.putVolumeLabel(buf[off:])
	} else {
		off += l.putDotEntries(buf[off:], dirClus, parentClus, n)
	}
	for i := range infos {
		off += l.putChildEntries(buf[off:], &infos[i])
	}
	l.writeClusters(dirClus, buf)
	return dirClus, nil
}

func (l *layouter) layoutFile(n *image.Node) (uint32, uint32, error) {
	var size int64
	if n.Content != nil {
		size = n.Content.Size()
	}
	cs := uint64(l.geo.clusterSizeBytes())
	nClus := (uint64(size) + cs - 1) / cs
	first, err := l.allocChain(nClus)
	if err != nil {
		return 0, 0, err
	}
	if n.Content != nil && size > 0 {
		buf := make([]byte, nClus*cs) // zero-padded tail
		if _, err := n.Content.ReadAt(buf[:size], 0); err != nil && err != io.EOF {
			return 0, 0, err
		}
		l.writeClusters(first, buf)
	}
	return first, uint32(size), nil
}

// --- directory entry writers ---

func (l *layouter) putVolumeLabel(b []byte) int {
	copy(b[0:11], pack83Label(l.label))
	b[11] = attrVolumeID
	d, t, _ := fatTime(l.deps.Clock.Now())
	le.PutUint16(b[22:], t)
	le.PutUint16(b[24:], d)
	return dirEntrySize
}

func (l *layouter) putDotEntries(b []byte, self, parent uint32, n *image.Node) int {
	d, t, tenth := fatTime(n.ModTime)
	writeDot := func(dst []byte, name string, clus uint32) {
		copy(dst[0:11], name)
		dst[11] = attrDirectory
		dst[13] = tenth
		le.PutUint16(dst[14:], t)
		le.PutUint16(dst[16:], d)
		le.PutUint16(dst[22:], t)
		le.PutUint16(dst[24:], d)
		le.PutUint16(dst[20:], uint16(clus>>16))
		le.PutUint16(dst[26:], uint16(clus))
	}
	writeDot(b[0:], ".          ", self)
	// ".." pointing at the root is stored as cluster 0 by convention.
	pp := parent
	if pp == rootCluster {
		pp = 0
	}
	writeDot(b[dirEntrySize:], "..         ", pp)
	return 2 * dirEntrySize
}

func (l *layouter) putChildEntries(b []byte, ci *childInfo) int {
	checksum := shortChecksum(ci.short)
	off := 0
	for _, e := range lfnEntries(ci.name, checksum) {
		copy(b[off:], e[:])
		off += dirEntrySize
	}
	s := b[off:]
	copy(s[0:11], ci.short)
	s[11] = ci.attr
	d, t, tenth := fatTime(ci.node.ModTime)
	s[13] = tenth
	le.PutUint16(s[14:], t)
	le.PutUint16(s[16:], d)
	le.PutUint16(s[18:], d)
	le.PutUint16(s[22:], t)
	le.PutUint16(s[24:], d)
	le.PutUint16(s[20:], uint16(ci.firstClus>>16))
	le.PutUint16(s[26:], uint16(ci.firstClus))
	le.PutUint32(s[28:], ci.size)
	return off + dirEntrySize
}

// --- raw writes ---

func (l *layouter) writeClusters(first uint32, data []byte) {
	cs := int(l.geo.clusterSizeBytes())
	clus := first
	for off := 0; off < len(data); off += cs {
		end := off + cs
		if end > len(data) {
			end = len(data)
		}
		sec := l.geo.clusterSector(clus)
		l.dev.WriteAt(data[off:end], int64(sec)*sectorSize)
		clus = l.fat[clus]
		if clus == fatEOC || clus == 0 {
			break
		}
	}
}

func (l *layouter) writeFATs() error {
	raw := make([]byte, l.geo.fatSizeSectors*sectorSize)
	for i, v := range l.fat {
		if uint64(i)*4+4 > uint64(len(raw)) {
			break
		}
		le.PutUint32(raw[i*4:], v&0x0FFFFFFF)
	}
	for f := uint64(0); f < numFATs; f++ {
		off := int64(reservedSecs+f*l.geo.fatSizeSectors) * sectorSize
		if _, err := l.dev.WriteAt(raw, off); err != nil {
			return err
		}
	}
	return nil
}

func (l *layouter) writeBootAndInfo() error {
	boot := l.bootSector()
	if _, err := l.dev.WriteAt(boot, 0); err != nil {
		return err
	}
	if _, err := l.dev.WriteAt(boot, backupBootSec*sectorSize); err != nil {
		return err
	}
	info := l.fsInfo()
	if _, err := l.dev.WriteAt(info, fsInfoSector*sectorSize); err != nil {
		return err
	}
	_, err := l.dev.WriteAt(info, (backupBootSec+fsInfoSector)*sectorSize)
	return err
}

func (l *layouter) bootSector() []byte {
	g := l.geo
	b := make([]byte, sectorSize)
	b[0], b[1], b[2] = 0xEB, 0x58, 0x90
	copy(b[3:11], "fsforge ")
	le.PutUint16(b[11:], sectorSize)
	b[13] = byte(g.secPerClus)
	le.PutUint16(b[14:], reservedSecs)
	b[16] = numFATs
	le.PutUint16(b[17:], 0) // root entry count (FAT32)
	le.PutUint16(b[19:], 0) // totSec16
	b[21] = mediaByte
	le.PutUint16(b[22:], 0) // FATSz16
	le.PutUint16(b[24:], 32)
	le.PutUint16(b[26:], 64)
	le.PutUint32(b[28:], 0) // hidden sectors
	le.PutUint32(b[32:], uint32(g.totalSectors))
	le.PutUint32(b[36:], uint32(g.fatSizeSectors))
	le.PutUint16(b[40:], 0) // ext flags
	le.PutUint16(b[42:], 0) // fs version
	le.PutUint32(b[44:], rootCluster)
	le.PutUint16(b[48:], fsInfoSector)
	le.PutUint16(b[50:], backupBootSec)
	b[64] = 0x80
	b[66] = 0x29
	u := l.deps.UUID.UUID()
	le.PutUint32(b[67:], le.Uint32(u[0:4]))
	copy(b[71:82], pack83Label(l.label))
	copy(b[82:90], "FAT32   ")
	b[510], b[511] = 0x55, 0xAA
	return b
}

func (l *layouter) fsInfo() []byte {
	b := make([]byte, sectorSize)
	le.PutUint32(b[0:], 0x41615252)
	le.PutUint32(b[484:], 0x61417272)
	free := l.geo.clusters - l.usedClusters
	le.PutUint32(b[488:], uint32(free))
	le.PutUint32(b[492:], l.nextFree)
	b[508], b[509], b[510], b[511] = 0x00, 0x00, 0x55, 0xAA
	return b
}

// lfnCount is the number of LFN entries a name needs (13 UTF-16 units each).
func lfnCount(name string) int {
	if _, ok := fits83(name); ok {
		return 0
	}
	n := len([]rune(name))
	return (n + 12) / 13
}

func pack83Label(label string) string {
	b := []byte("           ")
	up := []byte(label)
	if len(up) > 11 {
		up = up[:11]
	}
	copy(b, up)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
	return string(b)
}

func sortedChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
