package exfat

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

// ExFAT is the exFAT engine.
type ExFAT struct{ deps image.Deps }

// New returns an exFAT engine wired with deps.
func New(deps image.Deps) *ExFAT {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	if deps.UUID == nil {
		deps.UUID = image.RandomUUID{}
	}
	return &ExFAT{deps: deps}
}

type exfatImage struct {
	*image.Mem
	dev   device.Device
	geo   geometry
	label string
	deps  image.Deps
}

// Format prepares a fresh exFAT volume on dev.
func (e *ExFAT) Format(dev device.Device, p image.Params) (image.Image, error) {
	geo, err := computeGeometry(dev.Size())
	if err != nil {
		return nil, err
	}
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &exfatImage{Mem: mem, dev: dev, geo: geo, label: p.Label, deps: e.deps}, nil
}

// Open is not supported yet; exFAT mutation will rebuild.
func (e *ExFAT) Open(device.Device) (image.Image, error) {
	return nil, errors.New("exfat: Open not supported (create-only)")
}

type layouter struct {
	dev   device.Device
	geo   geometry
	deps  image.Deps
	label string

	bitmap       []byte   // one bit per cluster
	fat          []uint32 // FAT entries (only chained objects use them)
	nextCluster  uint32
	usedClusters uint64

	bitmapFirst    uint32
	bitmapBytes    uint64
	upcaseFirst    uint32
	upcaseData     []byte
	upcaseChecksum uint32
}

// Finalize writes the exFAT volume deterministically.
func (img *exfatImage) Finalize() error {
	g := img.geo
	l := &layouter{
		dev:         img.dev,
		geo:         g,
		deps:        img.deps,
		label:       img.label,
		bitmap:      make([]byte, (g.clusterCount+7)/8),
		fat:         make([]uint32, g.clusterCount+2),
		nextCluster: 2,
	}
	l.fat[0] = 0xFFFFFFF8
	l.fat[1] = 0xFFFFFFFF

	// The allocation bitmap, up-case table and root directory have no NoFatChain
	// flag, so their clusters must be chained in the FAT.
	l.bitmapBytes = (g.clusterCount + 7) / 8
	var n uint64
	l.bitmapFirst, n = l.alloc(l.bitmapBytes)
	l.chain(l.bitmapFirst, n)
	l.upcaseData = buildUpcaseTable()
	l.upcaseChecksum = checksum32(l.upcaseData, 0)
	l.upcaseFirst, n = l.alloc(uint64(len(l.upcaseData)))
	l.chain(l.upcaseFirst, n)

	rootFirst, _, err := l.layoutDir(img.RootNode(), true)
	if err != nil {
		return err
	}

	// Write bitmap and up-case payloads.
	l.writeClusterData(l.bitmapFirst, l.bitmap, l.bitmapBytes)
	l.writeClusterData(l.upcaseFirst, l.upcaseData, uint64(len(l.upcaseData)))
	if err := l.writeFAT(); err != nil {
		return err
	}
	return l.writeBoot(rootFirst)
}

// alloc reserves enough contiguous clusters for nbytes, marks them in the
// bitmap, and returns the first cluster and the cluster count.
func (l *layouter) alloc(nbytes uint64) (uint32, uint64) {
	nclus := (nbytes + l.geo.clusterBytes() - 1) / l.geo.clusterBytes()
	if nclus == 0 {
		nclus = 1
	}
	first := l.nextCluster
	for i := uint64(0); i < nclus; i++ {
		idx := uint64(first-2) + i
		l.bitmap[idx/8] |= 1 << (idx % 8)
	}
	l.nextCluster += uint32(nclus)
	l.usedClusters += nclus
	return first, nclus
}

// chain links nclus contiguous clusters in the FAT, terminating with EOC. Used
// for objects without the NoFatChain flag (bitmap, up-case table, root dir).
func (l *layouter) chain(first uint32, nclus uint64) {
	for i := uint64(0); i < nclus; i++ {
		c := first + uint32(i)
		if i+1 < nclus {
			l.fat[c] = c + 1
		} else {
			l.fat[c] = 0xFFFFFFFF
		}
	}
}

type childInfo struct {
	node       *image.Node
	name       string
	firstClus  uint32
	dataLength uint64
}

func (l *layouter) layoutDir(n *image.Node, isRoot bool) (uint32, uint64, error) {
	children := sortedChildren(n)
	infos := make([]childInfo, 0, len(children))
	entries := 0
	if isRoot {
		entries += 3 // volume label + bitmap + up-case
	}
	for _, e := range children {
		switch {
		case e.Node.Mode&fs.ModeSymlink != 0:
			return 0, 0, fmt.Errorf("exfat: symlink %q not representable", e.Name)
		case e.Node.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
			return 0, 0, fmt.Errorf("exfat: special file %q not representable", e.Name)
		}
		infos = append(infos, childInfo{node: e.Node, name: e.Name})
		entries += 2 + nameEntries(e.Name) // file + stream + name entries
	}

	dirBytes := uint64(entries+1) * dirEntrySize // +1 for the end-of-directory marker
	dirFirst, dirClusters := l.alloc(dirBytes)
	if isRoot {
		l.chain(dirFirst, dirClusters) // the root directory has no NoFatChain flag
	}

	for i := range infos {
		ci := &infos[i]
		if ci.node.IsDir() {
			fc, dl, err := l.layoutDir(ci.node, false)
			if err != nil {
				return 0, 0, err
			}
			ci.firstClus, ci.dataLength = fc, dl
		} else {
			fc, dl, err := l.layoutFile(ci.node)
			if err != nil {
				return 0, 0, err
			}
			ci.firstClus, ci.dataLength = fc, dl
		}
	}

	buf := make([]byte, l.alignClusters(dirBytes))
	off := 0
	if isRoot {
		off += l.putVolumeLabel(buf[off:])
		off += l.putBitmapEntry(buf[off:])
		off += l.putUpcaseEntry(buf[off:])
	}
	for i := range infos {
		off += l.putFileSet(buf[off:], &infos[i])
	}
	l.writeClusterRaw(dirFirst, buf)
	// A directory's DataLength is its whole-cluster allocation size.
	return dirFirst, l.alignClusters(dirBytes), nil
}

func (l *layouter) layoutFile(n *image.Node) (uint32, uint64, error) {
	var size uint64
	if n.Content != nil {
		size = uint64(n.Content.Size())
	}
	if size == 0 {
		return 0, 0, nil
	}
	first, _ := l.alloc(size)
	buf := make([]byte, l.alignClusters(size))
	if _, err := n.Content.ReadAt(buf[:size], 0); err != nil && err != io.EOF {
		return 0, 0, err
	}
	l.writeClusterRaw(first, buf)
	return first, size, nil
}

func (l *layouter) alignClusters(nbytes uint64) uint64 {
	cb := l.geo.clusterBytes()
	n := (nbytes + cb - 1) / cb
	if n == 0 {
		n = 1
	}
	return n * cb
}

// --- directory entry encoders ---

func (l *layouter) putVolumeLabel(b []byte) int {
	units := nameUTF16(l.label)
	if len(units) > 11 {
		units = units[:11]
	}
	b[0] = entVolumeLabel
	b[1] = byte(len(units))
	for i, u := range units {
		le.PutUint16(b[2+i*2:], u)
	}
	return dirEntrySize
}

func (l *layouter) putBitmapEntry(b []byte) int {
	b[0] = entBitmap
	le.PutUint32(b[20:], l.bitmapFirst)
	le.PutUint64(b[24:], l.bitmapBytes)
	return dirEntrySize
}

func (l *layouter) putUpcaseEntry(b []byte) int {
	b[0] = entUpcase
	le.PutUint32(b[4:], l.upcaseChecksum)
	le.PutUint32(b[20:], l.upcaseFirst)
	le.PutUint64(b[24:], uint64(len(l.upcaseData)))
	return dirEntrySize
}

func (l *layouter) putFileSet(b []byte, ci *childInfo) int {
	units := nameUTF16(ci.name)
	nName := (len(units) + 14) / 15
	secondary := 1 + nName
	total := (1 + secondary) * dirEntrySize
	set := b[:total]

	// File entry.
	set[0] = entFile
	set[1] = byte(secondary)
	le.PutUint16(set[4:], fileAttr(ci.node.IsDir()))
	cT, cM, cU := timestamp(ci.node.ModTime)
	le.PutUint32(set[8:], cT)
	le.PutUint32(set[12:], cT)
	le.PutUint32(set[16:], cT)
	set[20], set[21] = cM, cM
	set[22], set[23], set[24] = cU, cU, cU

	// Stream extension entry.
	s := set[dirEntrySize:]
	s[0] = entStream
	if ci.dataLength > 0 {
		s[1] = flagAllocPossible | flagNoFatChain
	} else {
		s[1] = flagAllocPossible
	}
	s[3] = byte(len(units))
	le.PutUint16(s[4:], nameHash(units))
	le.PutUint64(s[8:], ci.dataLength) // valid data length
	le.PutUint32(s[20:], ci.firstClus)
	le.PutUint64(s[24:], ci.dataLength)

	// File name entries.
	for k := 0; k < nName; k++ {
		ne := set[(2+k)*dirEntrySize:]
		ne[0] = entFileName
		chunk := units[k*15:]
		if len(chunk) > 15 {
			chunk = chunk[:15]
		}
		for j, u := range chunk {
			le.PutUint16(ne[2+j*2:], u)
		}
	}

	le.PutUint16(set[2:], setChecksum(set))
	return total
}

// --- raw writes ---

func (l *layouter) writeClusterRaw(first uint32, data []byte) {
	l.dev.WriteAt(data, int64(l.geo.clusterSector(first))*bytesPerSector)
}

func (l *layouter) writeClusterData(first uint32, data []byte, _ uint64) {
	buf := make([]byte, l.alignClusters(uint64(len(data))))
	copy(buf, data)
	l.writeClusterRaw(first, buf)
}

func (l *layouter) writeFAT() error {
	raw := make([]byte, l.geo.fatLength*bytesPerSector)
	for i, v := range l.fat {
		if i*4+4 > len(raw) {
			break
		}
		le.PutUint32(raw[i*4:], v)
	}
	_, err := l.dev.WriteAt(raw, int64(l.geo.fatOffset)*bytesPerSector)
	return err
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
