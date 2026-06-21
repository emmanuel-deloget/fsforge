package romfs

import (
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Romfs is the romfs engine, implementing image.Filesystem. Format produces a
// big-endian, uncompressed read-only image the Linux kernel mounts. Open parses
// such an image back into the tree, so romfs doubles as a conversion source; the
// opened image is read-only (romfs is write-once), so its Finalize reports that
// it cannot be re-finalized.
type Romfs struct {
	deps image.Deps
}

// New returns a romfs engine wired with deps. A nil Clock is replaced with the
// host system clock (romfs stores no timestamps, so it is otherwise unused).
func New(deps image.Deps) *Romfs {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	return &Romfs{deps: deps}
}

type rImage struct {
	*image.Mem
	dev   device.Device
	label string
}

// Format starts a fresh romfs image on dev.
func (e *Romfs) Format(dev device.Device, p image.Params) (image.Image, error) {
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &rImage{Mem: mem, dev: dev, label: p.Label}, nil
}

// Finalize serialises the tree into a romfs image on the device.
func (img *rImage) Finalize() error {
	w := newRwriter(img.dev, img.label)
	return w.write(img.RootNode())
}
