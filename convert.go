package fsforge

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/emmanuel-deloget/fsforge/pkg/cpio"
	"github.com/emmanuel-deloget/fsforge/pkg/cramfs"
	"github.com/emmanuel-deloget/fsforge/pkg/erofs"
	"github.com/emmanuel-deloget/fsforge/pkg/exfat"
	"github.com/emmanuel-deloget/fsforge/pkg/ext"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/iso"
	"github.com/emmanuel-deloget/fsforge/pkg/oci"
	"github.com/emmanuel-deloget/fsforge/pkg/romfs"
	"github.com/emmanuel-deloget/fsforge/pkg/squashfs"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
	"github.com/emmanuel-deloget/fsforge/pkg/udf"
)

// Location names one end of a conversion: a Kind (dir, ext2, ext4, squashfs,
// erofs, oci, fat, exfat, iso) and a filesystem Path.
type Location struct {
	Kind string
	Path string
}

// Options tunes a Convert. The zero value is valid: it builds a host
// (non-reproducible) image with engine-default block size.
type Options struct {
	// Deps selects reproducible vs host behaviour. A zero Deps defaults to
	// HostDeps.
	Deps image.Deps
	// Size is required for fixed-size sinks (ext, fat, exfat), e.g. "512M".
	Size string
	// BlockSize overrides the engine default when non-zero.
	BlockSize uint32
	// Ref is the image reference recorded for an oci sink (default
	// "fsforge:latest").
	Ref string
}

// Convert bridges any supported source to any supported sink through the shared
// tree model: dir/ext2/ext4/squashfs/exfat/iso/oci → tree → dir/ext/squashfs/
// fat/exfat/iso/oci. File contents are streamed, not buffered.
func Convert(from, to Location, opt Options) error {
	if opt.Deps.Clock == nil {
		opt.Deps = HostDeps()
	}
	if opt.Ref == "" {
		opt.Ref = "fsforge:latest"
	}

	root, cfg, cleanup, err := loadTree(from.Kind, from.Path, opt.Deps)
	if err != nil {
		return fmt.Errorf("load %s:%s: %w", from.Kind, from.Path, err)
	}
	defer cleanup()

	if err := writeTree(root, to, cfg, opt); err != nil {
		return fmt.Errorf("write %s:%s: %w", to.Kind, to.Path, err)
	}
	return nil
}

type rootNoder interface{ RootNode() *image.Node }

// loadTree produces a tree root from a source location. The returned cleanup
// must be called after the tree has been consumed (it closes backing handles).
func loadTree(kind, path string, deps image.Deps) (*image.Node, *oci.Image, func(), error) {
	noop := func() {}
	switch kind {
	case "dir":
		mem := image.NewMem(deps, tree.Meta{Mode: fs.ModeDir | 0o755})
		closer, err := PopulateFromDir(mem.Root(), path)
		cleanup := func() { closer.Close() }
		if err != nil {
			cleanup()
			return nil, nil, noop, err
		}
		return mem.RootNode(), nil, cleanup, nil

	case "ext2", "ext4":
		return openImage(path, ext.NewExt2(deps)) // Open recovers the variant

	case "squashfs":
		return openImage(path, squashfs.New(deps))

	case "exfat":
		return openImage(path, exfat.New(deps))

	case "iso", "iso9660":
		return openImage(path, iso.New(deps))

	case "erofs":
		return openImage(path, erofs.New(deps))

	case "cpio", "initramfs":
		return openImage(path, cpio.New(deps))

	case "udf":
		return openImage(path, udf.New(deps))

	case "cramfs":
		return openImage(path, cramfs.New(deps))

	case "romfs":
		return openImage(path, romfs.New(deps))

	case "oci":
		l, err := oci.OpenLayout(path)
		if err != nil {
			return nil, nil, noop, err
		}
		mem, cfg, cleanup, err := oci.Flatten(l, "", deps)
		if err != nil {
			return nil, nil, noop, err
		}
		return mem.RootNode(), &cfg, cleanup, nil

	default:
		return nil, nil, noop, fmt.Errorf("unknown source kind %q", kind)
	}
}

// openImage opens an on-disk image with eng and returns its tree root. A QCOW2
// container is decoded transparently, so a filesystem stored inside one opens
// like a raw image.
func openImage(path string, eng image.Filesystem) (*image.Node, *oci.Image, func(), error) {
	noop := func() {}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, noop, err
	}
	dev, err := inputDevice(f)
	if err != nil {
		f.Close()
		return nil, nil, noop, err
	}
	img, err := eng.Open(dev)
	if err != nil {
		f.Close()
		return nil, nil, noop, err
	}
	return img.(rootNoder).RootNode(), nil, func() { f.Close() }, nil
}

// writeTree consumes a tree root into a sink location.
func writeTree(root *image.Node, to Location, cfg *oci.Image, opt Options) error {
	switch to.Kind {
	case "dir":
		return ExtractToDir(root, to.Path)

	case "ext2", "ext4", "squashfs", "fat", "fat32", "exfat", "iso", "iso9660", "erofs", "cpio", "initramfs", "udf", "cramfs", "romfs":
		b := &Builder{fstype: to.Kind, deps: opt.Deps, size: opt.Size, blockSize: opt.BlockSize}
		return b.BuildFromTree(root, to.Path)

	case "oci":
		l, err := oci.CreateLayout(to.Path)
		if err != nil {
			return err
		}
		mem := image.Adopt(opt.Deps, root)
		bo := oci.BuildOptions{Ref: opt.Ref, Gzip: true}
		if cfg != nil { // carry runtime config across an oci->oci conversion
			bo.Architecture = cfg.Architecture
			bo.OS = cfg.OS
			bo.Config = cfg.Config
		}
		_, err = oci.Build(l, mem, bo)
		return err

	default:
		return fmt.Errorf("unknown sink kind %q", to.Kind)
	}
}
