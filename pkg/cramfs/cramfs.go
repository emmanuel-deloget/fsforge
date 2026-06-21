package cramfs

import (
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Cramfs is the cramfs engine, implementing image.Filesystem. Format produces a
// little-endian, zlib-compressed read-only image the Linux kernel mounts. Open
// parses such an image back into the tree, so cramfs doubles as a conversion
// source; the opened image is read-only (cramfs is write-once, so mutation means
// rebuilding), so its Finalize reports that it cannot be re-finalized.
type Cramfs struct {
	deps image.Deps
}

// New returns a cramfs engine wired with deps. A nil Clock is replaced with the
// host system clock (cramfs stores no timestamps, so it is unused beyond the
// dependency-injection contract).
func New(deps image.Deps) *Cramfs {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	return &Cramfs{deps: deps}
}

type cImage struct {
	*image.Mem
	dev   device.Device
	label string
}

// Format starts a fresh cramfs image on dev.
func (e *Cramfs) Format(dev device.Device, p image.Params) (image.Image, error) {
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &cImage{Mem: mem, dev: dev, label: p.Label}, nil
}

// Finalize serialises the tree into a cramfs image on the device.
func (img *cImage) Finalize() error {
	w := newCwriter(img.dev, img.label)
	return w.write(img.RootNode())
}
