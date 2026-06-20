package squashfs

import (
	"errors"
	"io/fs"
	"math/bits"

	"github.com/emmanuel-deloget/fsforge/pkg/compress"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Squashfs is the squashfs 4.0 engine. It produces non-fragmented, gzip (by
// default) compressed images. Being a write-once format, "mutation" means
// rebuilding, so only Format is implemented here.
type Squashfs struct {
	deps      image.Deps
	comp      compress.Compressor
	blockSize uint32
}

// Option configures the engine.
type Option func(*Squashfs)

// WithCompressor selects the data/metadata codec (default gzip).
func WithCompressor(c compress.Compressor) Option { return func(s *Squashfs) { s.comp = c } }

// WithBlockSize sets the data block size (power of two, default 128 KiB).
func WithBlockSize(bs uint32) Option { return func(s *Squashfs) { s.blockSize = bs } }

// New returns a squashfs engine wired with deps.
func New(deps image.Deps, opts ...Option) *Squashfs {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	s := &Squashfs{deps: deps, comp: compress.Zlib{}, blockSize: defaultBlockSize}
	for _, o := range opts {
		o(s)
	}
	return s
}

type sqImage struct {
	*image.Mem
	dev device.Device
	eng *Squashfs
}

// Format starts a fresh squashfs image on dev.
func (e *Squashfs) Format(dev device.Device, _ image.Params) (image.Image, error) {
	if bits.OnesCount32(e.blockSize) != 1 || e.blockSize < 4096 {
		return nil, errors.New("squashfs: block size must be a power of two >= 4096")
	}
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &sqImage{Mem: mem, dev: dev, eng: e}, nil
}

// Finalize serialises the tree into a squashfs archive on the device.
func (img *sqImage) Finalize() error {
	w := newSwriter(img.dev, img.eng.comp, img.eng.blockSize, img.eng.deps.Clock)
	root := img.RootNode()
	w.assignInos(root)
	inodesCount := w.nextIno - 1

	rootRes, err := w.writeNode(root, 0)
	if err != nil {
		return err
	}
	w.inodes.finish()
	w.dirs.finish()

	inodeTableStart := uint64(w.pos)
	w.writeAt(w.inodes.out)
	dirTableStart := uint64(w.pos)
	w.writeAt(w.dirs.out)
	idTableStart, noIDs := w.writeIDTable()
	bytesUsed := uint64(w.pos)

	if w.err != nil {
		return w.err
	}

	sb := superblock{
		inodes:           inodesCount,
		mkfsTime:         uint32(img.eng.deps.Clock.Now().Unix()),
		blockSize:        img.eng.blockSize,
		fragments:        0,
		compression:      img.eng.comp.ID(),
		blockLog:         uint16(bits.TrailingZeros32(img.eng.blockSize)),
		flags:            flagNoFragments | flagNoXattrs,
		noIDs:            noIDs,
		rootInode:        inodeRef(rootRes.block, rootRes.offset),
		bytesUsed:        bytesUsed,
		idTableStart:     idTableStart,
		xattrTableStart:  noTable,
		inodeTableStart:  inodeTableStart,
		dirTableStart:    dirTableStart,
		fragTableStart:   noTable,
		lookupTableStart: noTable,
	}
	if _, err := img.dev.WriteAt(sb.marshal(), 0); err != nil {
		return err
	}
	return nil
}
