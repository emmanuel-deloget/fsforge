package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/ext"
	"github.com/emmanuel-deloget/fsforge/pkg/fat"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/squashfs"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func mkfs(args []string) error {
	fsSet := flag.NewFlagSet("mkfs", flag.ContinueOnError)
	typ := fsSet.String("type", "", "filesystem type: ext2, ext4, squashfs")
	source := fsSet.String("source", "", "source directory")
	output := fsSet.String("output", "", "output image file")
	sizeStr := fsSet.String("size", "", "image size (ext only), e.g. 64M")
	blockSize := fsSet.Uint("block-size", 0, "block size in bytes")
	label := fsSet.String("label", "", "volume label")
	reproducible := fsSet.Bool("reproducible", false, "deterministic output")
	if err := fsSet.Parse(args); err != nil {
		return err
	}
	if *typ == "" || *source == "" || *output == "" {
		return fmt.Errorf("-type, -source and -output are required")
	}

	deps := buildDeps(*reproducible)
	eng, err := engineFor(*typ, deps, uint32(*blockSize))
	if err != nil {
		return err
	}

	// Size the backing device. ext needs an explicit, block-aligned size;
	// squashfs is sized generously from the input and trimmed afterwards.
	size, err := deviceSize(*typ, *sizeStr, *source, uint32(*blockSize))
	if err != nil {
		return err
	}

	f, err := os.Create(*output)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		return err
	}
	dev := device.NewFile(f, size)

	img, err := eng.Format(dev, image.Params{Label: *label, BlockSize: uint32(*blockSize)})
	if err != nil {
		return err
	}
	closers, err := populate(img.Root(), *source)
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	if err != nil {
		return err
	}
	if err := img.Finalize(); err != nil {
		return err
	}

	if *typ == "squashfs" {
		if err := trimSquashfs(f); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %s image %s\n", *typ, *output)
	return nil
}

func buildDeps(reproducible bool) image.Deps {
	if !reproducible {
		return image.Deps{Clock: image.SystemClock{}, UUID: image.RandomUUID{}}
	}
	epoch := int64(0)
	if v := os.Getenv("SOURCE_DATE_EPOCH"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			epoch = n
		}
	}
	return image.Deps{
		Clock: image.FixedClock{T: time.Unix(epoch, 0).UTC()},
		UUID:  image.FixedUUID{},
	}
}

func engineFor(typ string, deps image.Deps, blockSize uint32) (image.Filesystem, error) {
	switch typ {
	case "ext2":
		return ext.NewExt2(deps), nil
	case "ext4":
		return ext.NewExt4(deps), nil
	case "fat", "fat32":
		return fat.New(deps), nil
	case "squashfs":
		var opts []squashfs.Option
		if blockSize != 0 {
			opts = append(opts, squashfs.WithBlockSize(blockSize))
		}
		return squashfs.New(deps, opts...), nil
	default:
		return nil, fmt.Errorf("unknown type %q", typ)
	}
}

func deviceSize(typ, sizeStr, source string, blockSize uint32) (int64, error) {
	if typ == "squashfs" {
		total, err := dirSize(source)
		if err != nil {
			return 0, err
		}
		return total + total/2 + (16 << 20), nil // generous headroom; trimmed later
	}
	if sizeStr == "" {
		return 0, fmt.Errorf("-size is required for %s", typ)
	}
	size, err := parseSize(sizeStr)
	if err != nil {
		return 0, err
	}
	bs := int64(blockSize)
	if bs == 0 {
		if typ == "ext4" {
			bs = 4096
		} else {
			bs = 1024
		}
	}
	return size - size%bs, nil
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "K"), strings.HasSuffix(s, "k"):
		mult, s = 1<<10, s[:len(s)-1]
	case strings.HasSuffix(s, "M"), strings.HasSuffix(s, "m"):
		mult, s = 1<<20, s[:len(s)-1]
	case strings.HasSuffix(s, "G"), strings.HasSuffix(s, "g"):
		mult, s = 1<<30, s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n * mult, nil
}

func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// populate mirrors a host directory into the image tree, returning the file
// handles to close after Finalize (contents are streamed, not buffered).
func populate(dir image.Dir, src string) ([]*os.File, error) {
	var closers []*os.File
	entries, err := os.ReadDir(src) // sorted by name
	if err != nil {
		return closers, err
	}
	for _, e := range entries {
		full := filepath.Join(src, e.Name())
		info, err := e.Info()
		if err != nil {
			return closers, err
		}
		m := tree.Meta{Mode: info.Mode(), ModTime: info.ModTime()}
		switch {
		case e.IsDir():
			sub, err := dir.Mkdir(e.Name(), m)
			if err != nil {
				return closers, err
			}
			cs, err := populate(sub, full)
			closers = append(closers, cs...)
			if err != nil {
				return closers, err
			}
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				return closers, err
			}
			if err := dir.Symlink(e.Name(), target, m); err != nil {
				return closers, err
			}
		case info.Mode().IsRegular():
			f, err := os.Open(full)
			if err != nil {
				return closers, err
			}
			closers = append(closers, f)
			if _, err := dir.Create(e.Name(), &osSource{f: f, size: info.Size()}, m); err != nil {
				return closers, err
			}
		}
	}
	return closers, nil
}

// osSource is a lazy tree.Source over a host file.
type osSource struct {
	f    *os.File
	size int64
}

func (s *osSource) Size() int64                             { return s.size }
func (s *osSource) ReadAt(p []byte, off int64) (int, error) { return s.f.ReadAt(p, off) }

// trimSquashfs shrinks the output file to the archive's bytes_used.
func trimSquashfs(f *os.File) error {
	hdr := make([]byte, 48)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		return err
	}
	bytesUsed := int64(binary.LittleEndian.Uint64(hdr[40:]))
	return f.Truncate(bytesUsed)
}
