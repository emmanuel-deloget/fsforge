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

// FAT is the FAT12/16/32 create engine, implementing image.Filesystem. The FAT
// width is chosen from the volume size unless forced with WithFATBits. It
// writes ESP/boot/data volumes (long file names with generated 8.3 aliases)
// whose images pass fsck.fat. Being a DOS-lineage format it has no owners,
// permissions or links. Open is not yet supported (mutation rebuilds).
type FAT struct {
	deps      image.Deps
	forceBits int
}

// Option configures a FAT engine; pass options to New.
type Option func(*FAT)

// WithFATBits forces the FAT type (12, 16 or 32) instead of auto-selecting by
// size. Useful for ESPs, which conventionally use FAT32 (or FAT16).
func WithFATBits(bits int) Option { return func(f *FAT) { f.forceBits = bits } }

// New returns a FAT engine wired with deps.
func New(deps image.Deps, opts ...Option) *FAT {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	if deps.UUID == nil {
		deps.UUID = image.RandomUUID{}
	}
	f := &FAT{deps: deps}
	for _, o := range opts {
		o(f)
	}
	return f
}

type fatImage struct {
	*image.Mem
	dev   device.Device
	geo   geometry
	label string
	deps  image.Deps
}

// Format prepares a fresh FAT volume sized to fill dev, choosing FAT12/16/32
// from the volume size (or the width forced via WithFATBits). p.Label sets the
// volume label, defaulting to "NO NAME". It fails if dev is too small for the
// selected FAT width.
func (e *FAT) Format(dev device.Device, p image.Params) (image.Image, error) {
	geo, err := computeGeometry(dev.Size(), e.forceBits)
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

// Finalize writes the FAT volume deterministically.
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
	// Reserved FAT entries 0 and 1.
	l.fat[0] = (0x0FFFFF00 | uint32(mediaByte)) & g.eoc()
	l.fat[1] = g.eoc()

	if g.fatBits == 32 {
		rootClus, err := l.layoutDir(img.RootNode(), 0, true)
		if err != nil {
			return err
		}
		if rootClus != rootCluster {
			return fmt.Errorf("fat: root cluster = %d, want %d", rootClus, rootCluster)
		}
	} else {
		if err := l.layoutRootFixed(img.RootNode()); err != nil {
			return err
		}
	}
	if err := l.writeFATs(); err != nil {
		return err
	}
	return l.writeBootAndInfo()
}

// layoutRootFixed lays out the FAT12/16 fixed-size root directory region.
func (l *layouter) layoutRootFixed(root *image.Node) error {
	namer := newShortNamer()
	infos, _, err := l.buildInfos(root, namer)
	if err != nil {
		return err
	}
	if err := l.layoutInfos(infos, 0); err != nil {
		return err
	}
	region := make([]byte, l.geo.rootDirSectors*sectorSize)
	off := l.putVolumeLabel(region)
	for i := range infos {
		if off+entryBytes(infos[i].name) > len(region) {
			return fmt.Errorf("fat: root directory full (%d entries max)", l.geo.rootEntCount)
		}
		off += l.putChildEntries(region[off:], &infos[i])
	}
	_, err = l.dev.WriteAt(region, int64(l.geo.rootRegionSector())*sectorSize)
	return err
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
			l.fat[c] = l.geo.eoc()
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
	infos, entryCount, err := l.buildInfos(n, namer)
	if err != nil {
		return 0, err
	}
	if !isRoot {
		entryCount += 2 // "." and ".."
	} else {
		entryCount++ // volume label entry
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

	dotdotForChildren := dirClus
	if isRoot {
		dotdotForChildren = 0
	}
	if err := l.layoutInfos(infos, dotdotForChildren); err != nil {
		return 0, err
	}

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

// buildInfos creates the child skeleton (names + generated 8.3 short names) and
// the entry count, without laying out child data yet.
func (l *layouter) buildInfos(n *image.Node, namer *shortNamer) ([]childInfo, int, error) {
	children := sortedChildren(n)
	infos := make([]childInfo, 0, len(children))
	count := 0
	for _, e := range children {
		switch {
		case e.Node.Mode&fs.ModeSymlink != 0:
			return nil, 0, fmt.Errorf("fat: symlink %q not representable in FAT", e.Name)
		case e.Node.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
			return nil, 0, fmt.Errorf("fat: special file %q not representable in FAT", e.Name)
		}
		var attr byte = attrArchive
		if e.Node.IsDir() {
			attr = attrDirectory
		}
		infos = append(infos, childInfo{node: e.Node, name: e.Name, short: namer.generate(e.Name), attr: attr})
		count += lfnCount(e.Name) + 1
	}
	return infos, count, nil
}

// layoutInfos lays out each child's data (recursing into directories), filling
// firstClus/size. dotdotForChildren is the cluster a child directory records in
// its ".." entry (0 when the parent is the root directory).
func (l *layouter) layoutInfos(infos []childInfo, dotdotForChildren uint32) error {
	for i := range infos {
		ci := &infos[i]
		if ci.node.IsDir() {
			fc, err := l.layoutDir(ci.node, dotdotForChildren, false)
			if err != nil {
				return err
			}
			ci.firstClus = fc
		} else {
			fc, sz, err := l.layoutFile(ci.node)
			if err != nil {
				return err
			}
			ci.firstClus, ci.size = fc, sz
		}
	}
	return nil
}

func entryBytes(name string) int { return (lfnCount(name) + 1) * dirEntrySize }

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
	// The caller already passes 0 for ".." when the parent is the root.
	writeDot(b[dirEntrySize:], "..         ", parent)
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
		if clus >= l.geo.eoc() || clus == 0 {
			break
		}
	}
}

func (l *layouter) writeFATs() error {
	raw := make([]byte, l.geo.fatSizeSectors*sectorSize)
	switch l.geo.fatBits {
	case 12:
		for i, v := range l.fat {
			packFAT12(raw, i, v&0x0FFF)
		}
	case 16:
		for i, v := range l.fat {
			if i*2+2 > len(raw) {
				break
			}
			le.PutUint16(raw[i*2:], uint16(v))
		}
	default:
		for i, v := range l.fat {
			if i*4+4 > len(raw) {
				break
			}
			le.PutUint32(raw[i*4:], v&0x0FFFFFFF)
		}
	}
	for f := uint64(0); f < numFATs; f++ {
		off := int64(l.geo.reservedSecs+f*l.geo.fatSizeSectors) * sectorSize
		if _, err := l.dev.WriteAt(raw, off); err != nil {
			return err
		}
	}
	return nil
}

// packFAT12 writes a 12-bit FAT entry for cluster i.
func packFAT12(raw []byte, i int, v uint32) {
	off := i * 3 / 2
	if off+1 >= len(raw) {
		return
	}
	if i%2 == 0 {
		raw[off] = byte(v)
		raw[off+1] = (raw[off+1] & 0xF0) | byte((v>>8)&0x0F)
	} else {
		raw[off] = (raw[off] & 0x0F) | byte((v&0x0F)<<4)
		raw[off+1] = byte(v >> 4)
	}
}

func (l *layouter) writeBootAndInfo() error {
	boot := l.bootSector()
	if _, err := l.dev.WriteAt(boot, 0); err != nil {
		return err
	}
	if l.geo.fatBits != 32 {
		return nil // FAT12/16 have no backup boot sector or FSInfo
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
	le.PutUint16(b[14:], uint16(g.reservedSecs))
	b[16] = numFATs
	le.PutUint16(b[17:], uint16(g.rootEntCount))
	if g.totalSectors < 0x10000 {
		le.PutUint16(b[19:], uint16(g.totalSectors)) // totSec16
	} else {
		le.PutUint32(b[32:], uint32(g.totalSectors)) // totSec32
	}
	b[21] = mediaByte
	le.PutUint16(b[24:], 32) // sectors per track
	le.PutUint16(b[26:], 64) // heads
	u := l.deps.UUID.UUID()

	if g.fatBits == 32 {
		le.PutUint16(b[22:], 0) // FATSz16 = 0
		le.PutUint32(b[36:], uint32(g.fatSizeSectors))
		le.PutUint32(b[44:], rootCluster)
		le.PutUint16(b[48:], fsInfoSector)
		le.PutUint16(b[50:], backupBootSec)
		b[64] = 0x80
		b[66] = 0x29
		le.PutUint32(b[67:], le.Uint32(u[0:4]))
		copy(b[71:82], pack83Label(l.label))
		copy(b[82:90], "FAT32   ")
	} else {
		le.PutUint16(b[22:], uint16(g.fatSizeSectors)) // FATSz16
		b[36] = 0x80                                   // drive number
		b[38] = 0x29                                   // extended boot signature
		le.PutUint32(b[39:], le.Uint32(u[0:4]))
		copy(b[43:54], pack83Label(l.label))
		if g.fatBits == 12 {
			copy(b[54:62], "FAT12   ")
		} else {
			copy(b[54:62], "FAT16   ")
		}
	}
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
