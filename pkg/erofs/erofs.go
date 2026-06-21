package erofs

import (
	"fmt"
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Erofs is the EROFS engine, implementing image.Filesystem. Format produces an
// uncompressed image (extended inodes, FLAT_PLAIN data, 4 KiB blocks) that the
// kernel and fsck.erofs accept. Open reads an existing image — including ones
// written by mkfs.erofs that use compact inodes and inline tails — into the
// tree, so EROFS doubles as a conversion source; the opened image is read-only
// because EROFS is write-once, so mutation means rebuilding via Convert.
type Erofs struct {
	deps image.Deps
}

// New returns an EROFS engine wired with deps. A nil Clock is replaced with the
// host system clock.
func New(deps image.Deps) *Erofs {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	if deps.UUID == nil {
		deps.UUID = image.RandomUUID{}
	}
	return &Erofs{deps: deps}
}

type eImage struct {
	*image.Mem
	dev   device.Device
	eng   *Erofs
	label string
}

// Format starts a fresh EROFS image on dev.
func (e *Erofs) Format(dev device.Device, p image.Params) (image.Image, error) {
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &eImage{Mem: mem, dev: dev, eng: e, label: p.Label}, nil
}

// Finalize serialises the tree into an EROFS image on the device.
func (img *eImage) Finalize() error {
	w := newEwriter(img.dev, img.eng.deps.Clock)
	root := img.RootNode()

	w.assignNids(root)
	metaEnd := w.nextOff
	rootNid := w.nids[root]
	if rootNid > 0xFFFF {
		return fmt.Errorf("erofs: root nid %d does not fit the 16-bit superblock field", rootNid)
	}
	w.dataStart = uint32((metaEnd + blockSize - 1) / blockSize)
	w.dataCursor = w.dataStart

	if err := w.layout(root, rootNid); err != nil {
		return err
	}
	if w.err != nil {
		return w.err
	}

	now := img.eng.deps.Clock.Now()
	sb := superblock{
		rootNid:     uint16(rootNid),
		inos:        uint64(w.inoCount),
		buildTime:   uint64(now.Unix()),
		buildNsec:   uint32(now.Nanosecond()),
		blocks:      w.dataCursor,
		metaBlkaddr: metaBlkAddr,
	}
	sb.uuid = img.eng.deps.UUID.UUID()
	copy(sb.volumeName[:], img.label)

	if _, err := img.dev.WriteAt(sb.marshal(), superOffset); err != nil {
		return err
	}
	return nil
}
