package cpio

import (
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Cpio is the cpio "newc" engine, implementing image.Filesystem. Format
// produces an uncompressed newc archive — the format the Linux kernel unpacks
// as an initramfs — validated against GNU cpio. Open parses a newc archive into
// the tree so cpio doubles as a conversion source; the opened archive is
// read-only (an archive is rewritten, not edited in place), so its Finalize
// reports that it cannot be re-finalized.
type Cpio struct {
	deps image.Deps
}

// New returns a cpio engine wired with deps. A nil Clock is replaced with the
// host system clock.
func New(deps image.Deps) *Cpio {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	return &Cpio{deps: deps}
}

type cImage struct {
	*image.Mem
	dev device.Device
	eng *Cpio
}

// Format starts a fresh cpio archive on dev.
func (e *Cpio) Format(dev device.Device, _ image.Params) (image.Image, error) {
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &cImage{Mem: mem, dev: dev, eng: e}, nil
}

// Finalize streams the tree into a newc archive on the device.
func (img *cImage) Finalize() error {
	w := newCwriter(img.dev, img.eng.deps.Clock)
	w.writeArchive(img.RootNode())
	return w.err
}
