package udf

import (
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// UDF is the UDF 2.01 engine, implementing image.Filesystem. Format produces a
// read-only UDF image (2048-byte blocks, a single Type-1 read-only partition,
// File Entries with short allocation descriptors) that the Linux kernel mounts
// and udfinfo reads. Open parses such an image back into the tree, so UDF
// doubles as a conversion source; the opened image is read-only, so its
// Finalize reports that it cannot be re-finalized in place.
type UDF struct {
	deps image.Deps
}

// New returns a UDF engine wired with deps. A nil Clock/UUID is replaced with
// the host defaults.
func New(deps image.Deps) *UDF {
	if deps.Clock == nil {
		deps.Clock = image.SystemClock{}
	}
	if deps.UUID == nil {
		deps.UUID = image.RandomUUID{}
	}
	return &UDF{deps: deps}
}

type uImage struct {
	*image.Mem
	dev   device.Device
	eng   *UDF
	label string
}

// Format starts a fresh UDF image on dev.
func (e *UDF) Format(dev device.Device, p image.Params) (image.Image, error) {
	label := p.Label
	if label == "" {
		label = "fsforge"
	}
	mem := image.NewMem(e.deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	return &uImage{Mem: mem, dev: dev, eng: e, label: label}, nil
}

// Finalize serialises the tree into a UDF image on the device.
func (img *uImage) Finalize() error {
	w := newUwriter(img.dev, img.eng.deps, img.label)
	return w.write(img.RootNode())
}
