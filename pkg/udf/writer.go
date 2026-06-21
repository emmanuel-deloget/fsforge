package udf

import (
	"fmt"
	"io"
	"io/fs"
	"sort"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

type uwriter struct {
	dev   device.Device
	deps  image.Deps
	label string
	now   time.Time
	uuid  [16]byte

	feLbn      map[*image.Node]uint32
	dataLbn    map[*image.Node]uint32
	dataBytes  map[*image.Node]uint64
	dataBlocks map[*image.Node]uint64
	uniqueID   map[*image.Node]uint64
	parent     map[*image.Node]*image.Node
	order      []*image.Node

	partitionLen      uint32
	numFiles, numDirs uint32
	nextUnique        uint64
	err               error
}

func newUwriter(dev device.Device, deps image.Deps, label string) *uwriter {
	return &uwriter{
		dev: dev, deps: deps, label: label,
		feLbn:      map[*image.Node]uint32{},
		dataLbn:    map[*image.Node]uint32{},
		dataBytes:  map[*image.Node]uint64{},
		dataBlocks: map[*image.Node]uint64{},
		uniqueID:   map[*image.Node]uint64{},
		parent:     map[*image.Node]*image.Node{},
	}
}

func (w *uwriter) write(root *image.Node) error {
	w.now = w.deps.Clock.Now()
	w.uuid = w.deps.UUID.UUID()

	// Pass 1: assign a File Entry block to every node (FSD takes partition
	// block 0, so File Entries start at block 1), pre-order.
	nextFE := uint32(1)
	w.assignFE(root, nil, &nextFE)

	// Pass 2: lay out each node's data after the File Entry region.
	cursor := 1 + uint32(len(w.order))
	for _, n := range w.order {
		var nbytes uint64
		switch {
		case n.IsDir():
			nbytes = uint64(len(w.buildDir(n, 0)))
		case n.Mode&fs.ModeSymlink != 0:
			nbytes = uint64(len(buildSymlink(n.Link)))
		case n.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
			nbytes = 0
		default:
			if n.Content != nil {
				nbytes = uint64(n.Content.Size())
			}
		}
		blocks := (nbytes + blockSize - 1) / blockSize
		w.dataBytes[n] = nbytes
		w.dataBlocks[n] = blocks
		if blocks > 0 {
			w.dataLbn[n] = cursor
			cursor += uint32(blocks)
		}
	}
	w.partitionLen = cursor

	// Unique IDs: the root directory is 0; everything else starts at 16.
	for i, n := range w.order {
		if i == 0 {
			w.uniqueID[n] = 0
		} else {
			w.uniqueID[n] = uint64(15 + i)
		}
		if n.IsDir() {
			w.numDirs++
		} else {
			w.numFiles++
		}
	}
	w.nextUnique = uint64(15 + len(w.order))

	total := uint32(partBlock) + w.partitionLen + 1 // AVDP copy at the last block

	w.writeVolumeStructures(total)
	w.writePartition()
	return w.err
}

// assignFE numbers a node's File Entry block and records its parent, recursing
// into directories. Hard-linked nodes keep a single File Entry.
func (w *uwriter) assignFE(n, parent *image.Node, next *uint32) {
	if _, ok := w.feLbn[n]; ok {
		return
	}
	w.feLbn[n] = *next
	w.parent[n] = parent
	w.order = append(w.order, n)
	*next++
	if n.IsDir() {
		for _, e := range sortedChildren(n) {
			w.assignFE(e.Node, n, next)
		}
	}
}

func (w *uwriter) writeVolumeStructures(total uint32) {
	// Volume Recognition Sequence.
	w.putVSD(vrsBlock+0, "BEA01")
	w.putVSD(vrsBlock+1, "NSR03")
	w.putVSD(vrsBlock+2, "TEA01")

	for _, base := range []uint32{mvdsBlock, rvdsBlock} {
		w.putAt(base+0, w.pvd(base+0))
		w.putAt(base+1, w.lvd(base+1))
		w.putAt(base+2, w.pd(base+2))
		w.putAt(base+3, w.usd(base+3))
		w.putAt(base+4, w.iuvd(base+4))
		w.putAt(base+5, w.td(base+5))
	}

	w.putAt(lvidBlock, w.lvid(lvidBlock))
	w.putAt(avdpBlock, w.avdp(avdpBlock))
	w.putAt(total-1, w.avdp(total-1))
}

func (w *uwriter) writePartition() {
	w.putAt(partBlock, w.fsd(0))
	for _, n := range w.order {
		w.putAt(partBlock+w.feLbn[n], w.fileEntry(n))
		w.writeNodeData(n)
	}
}

// fileEntry renders the File Entry for n, including its allocation descriptors
// and (for device nodes) the device-specification extended attribute.
func (w *uwriter) fileEntry(n *image.Node) []byte {
	lbn := w.feLbn[n]
	var ea, ads []byte
	if n.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0 {
		ea = w.deviceEA(n, lbn)
	}
	if dl := w.dataLbn[n]; w.dataBlocks[n] > 0 {
		ads = shortADs(dl, w.dataBytes[n])
	}
	return w.buildFE(n, lbn, w.dataBytes[n], w.dataBlocks[n], ads, ea)
}

// writeNodeData writes a node's data extent (directory FIDs, file contents or
// symlink path components) into the partition.
func (w *uwriter) writeNodeData(n *image.Node) {
	if w.dataBlocks[n] == 0 {
		return
	}
	abs := partBlock + w.dataLbn[n]
	switch {
	case n.IsDir():
		w.putBytes(abs, w.buildDir(n, w.dataLbn[n]))
	case n.Mode&fs.ModeSymlink != 0:
		w.putBytes(abs, buildSymlink(n.Link))
	default:
		w.streamContent(abs, n)
	}
}

// streamContent copies a regular file's contents into its data extent.
func (w *uwriter) streamContent(absBlock uint32, n *image.Node) {
	size := int64(w.dataBytes[n])
	buf := make([]byte, blockSize)
	off := int64(0)
	for off < size {
		nb := int64(blockSize)
		if rem := size - off; rem < nb {
			nb = rem
		}
		for i := range buf {
			buf[i] = 0
		}
		if _, err := n.Content.ReadAt(buf[:nb], off); err != nil && err != io.EOF {
			w.err = err
		}
		w.writeAt(buf, int64(absBlock)*blockSize+off)
		off += nb
	}
}

// --- block IO helpers ---

func (w *uwriter) putVSD(block uint32, ident string) {
	b := make([]byte, blockSize)
	copy(b[1:], ident)
	b[6] = 1 // structVersion
	w.putAt(block, b)
}

func (w *uwriter) putAt(block uint32, desc []byte) {
	w.writeAt(desc, int64(block)*blockSize)
}

func (w *uwriter) putBytes(absBlock uint32, data []byte) {
	w.writeAt(data, int64(absBlock)*blockSize)
}

func (w *uwriter) writeAt(p []byte, off int64) {
	if w.err != nil {
		return
	}
	if _, err := w.dev.WriteAt(p, off); err != nil {
		w.err = err
	}
}

func sortedChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// volSetIdent builds the volume set identifier: 16 unique hex digits derived
// from the volume UUID, then the label.
func (w *uwriter) volSetIdent() string {
	return fmt.Sprintf("%08x%08x%s", le.Uint32(w.uuid[0:]), le.Uint32(w.uuid[4:]), w.label)
}
