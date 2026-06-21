package fsforge

import (
	"os"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// Builder describes a single filesystem image to produce. The zero value is not
// usable; start from New. Configuration methods return the receiver so calls
// chain:
//
//	err := fsforge.New("ext4").Reproducible(0).Size("256M").Label("root").
//		BuildFromDir("./rootfs", "root.img")
type Builder struct {
	fstype    string
	deps      image.Deps
	size      string
	label     string
	blockSize uint32
}

// New starts a Builder for the given filesystem type (ext2, ext4, fat, exfat,
// iso or squashfs). It defaults to a host (non-reproducible) build; call
// Reproducible to make the output deterministic.
func New(fstype string) *Builder {
	return &Builder{fstype: fstype, deps: HostDeps()}
}

// Host selects a non-reproducible build using the system clock and random
// UUIDs. This is the default.
func (b *Builder) Host() *Builder {
	b.deps = HostDeps()
	return b
}

// Reproducible selects a deterministic build: timestamps are fixed at the given
// Unix epoch and the filesystem UUID is zeroed. Pass fsforge.SourceDateEpoch()
// to honour SOURCE_DATE_EPOCH.
func (b *Builder) Reproducible(epoch int64) *Builder {
	b.deps = ReproducibleDeps(epoch)
	return b
}

// Deps overrides the injected dependencies wholesale, for callers that wire
// their own Clock/UUID/Alloc. Most callers use Host or Reproducible instead.
func (b *Builder) Deps(d image.Deps) *Builder {
	b.deps = d
	return b
}

// Size sets the image size for fixed-size targets (ext, fat, exfat), e.g.
// "256M". It is ignored by content-sized targets (squashfs, iso), which are
// sized from their input and trimmed.
func (b *Builder) Size(s string) *Builder {
	b.size = s
	return b
}

// BlockSize sets the filesystem block size in bytes; zero uses the engine
// default.
func (b *Builder) BlockSize(n uint32) *Builder {
	b.blockSize = n
	return b
}

// Label sets the volume label.
func (b *Builder) Label(s string) *Builder {
	b.label = s
	return b
}

// BuildFromDir creates the image from the contents of the host directory
// srcDir and writes it to outPath. File contents are streamed, never buffered
// in full.
func (b *Builder) BuildFromDir(srcDir, outPath string) error {
	contentBytes, err := dirBytes(srcDir)
	if err != nil {
		return err
	}
	return b.build(outPath, contentBytes, func(root image.Dir) (func() error, error) {
		closer, err := PopulateFromDir(root, srcDir)
		return closer.Close, err
	})
}

// BuildFromTree creates the image from an in-memory node tree (for example one
// produced by another engine's Open or by oci.Flatten) and writes it to
// outPath.
func (b *Builder) BuildFromTree(root *image.Node, outPath string) error {
	return b.build(outPath, treeBytes(root), func(dst image.Dir) (func() error, error) {
		return func() error { return nil }, Graft(dst, root)
	})
}

// build is the shared create pipeline: size the device, format, fill via the
// supplied populate step, finalize, then trim content-sized formats.
func (b *Builder) build(outPath string, contentBytes int64, fill func(image.Dir) (func() error, error)) error {
	eng, err := EngineFor(b.fstype, b.deps, b.blockSize)
	if err != nil {
		return err
	}
	size, err := deviceSize(b.fstype, b.size, contentBytes, b.blockSize)
	if err != nil {
		return err
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	dev, finalize, err := outputBackend(b.fstype, outPath, f, size)
	if err != nil {
		return err
	}

	img, err := eng.Format(dev, image.Params{Label: b.label, BlockSize: b.blockSize})
	if err != nil {
		return err
	}
	done, err := fill(img.Root())
	defer done()
	if err != nil {
		return err
	}
	if err := img.Finalize(); err != nil {
		return err
	}
	return finalize()
}
