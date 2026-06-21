package fsforge

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/exfat"
	"github.com/emmanuel-deloget/fsforge/pkg/ext"
	"github.com/emmanuel-deloget/fsforge/pkg/fat"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/iso"
	"github.com/emmanuel-deloget/fsforge/pkg/squashfs"
)

// EngineFor returns the image.Filesystem engine for a filesystem type name,
// wired with deps. Recognised types: ext2, ext4, fat (fat32), exfat, iso
// (iso9660) and squashfs. blockSize, when non-zero, is forwarded to engines
// that accept one (currently squashfs).
func EngineFor(fstype string, deps image.Deps, blockSize uint32) (image.Filesystem, error) {
	switch fstype {
	case "ext2":
		return ext.NewExt2(deps), nil
	case "ext4":
		return ext.NewExt4(deps), nil
	case "fat", "fat32":
		return fat.New(deps), nil
	case "exfat":
		return exfat.New(deps), nil
	case "iso", "iso9660":
		return iso.New(deps), nil
	case "squashfs":
		var opts []squashfs.Option
		if blockSize != 0 {
			opts = append(opts, squashfs.WithBlockSize(blockSize))
		}
		return squashfs.New(deps, opts...), nil
	default:
		return nil, fmt.Errorf("unknown filesystem type %q", fstype)
	}
}

// sizedFromContent reports whether an engine is sized from its input and
// trimmed afterwards (squashfs, iso) rather than from an explicit -size.
func sizedFromContent(fstype string) bool {
	switch fstype {
	case "squashfs", "iso", "iso9660":
		return true
	}
	return false
}

// defaultBlockSize is the block size assumed when none is given, used only to
// round explicit sizes down to a whole number of blocks.
func defaultBlockSize(fstype string) int64 {
	if fstype == "ext4" {
		return 4096
	}
	return 1024
}

// deviceSize chooses the backing device size in bytes. Content-sized engines
// derive it from contentBytes with generous, later-trimmed headroom; the rest
// require an explicit sizeStr, rounded down to a whole number of blocks.
func deviceSize(fstype, sizeStr string, contentBytes int64, blockSize uint32) (int64, error) {
	if sizedFromContent(fstype) {
		return contentBytes + contentBytes/2 + (16 << 20), nil
	}
	if sizeStr == "" {
		return 0, fmt.Errorf("a size is required for %s (e.g. 256M)", fstype)
	}
	size, err := ParseSize(sizeStr)
	if err != nil {
		return 0, err
	}
	bs := int64(blockSize)
	if bs == 0 {
		bs = defaultBlockSize(fstype)
	}
	return size - size%bs, nil
}

// trim shrinks an output file to the bytes the engine actually used, for
// content-sized formats. It is a no-op for the others.
func trim(fstype string, f *os.File) error {
	switch fstype {
	case "squashfs":
		return trimSquashfs(f)
	case "iso", "iso9660":
		return trimISO(f)
	}
	return nil
}

// trimSquashfs shrinks the output file to the archive's bytes_used.
func trimSquashfs(f *os.File) error {
	hdr := make([]byte, 48)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		return err
	}
	bytesUsed := int64(binary.LittleEndian.Uint64(hdr[40:]))
	return f.Truncate(bytesUsed)
}

// trimISO shrinks the output to the ISO volume space (PVD volume space size in
// logical blocks of 2048 bytes, at sector 16 offset 80).
func trimISO(f *os.File) error {
	b := make([]byte, 4)
	if _, err := f.ReadAt(b, 16*2048+80); err != nil {
		return err
	}
	return f.Truncate(int64(binary.LittleEndian.Uint32(b)) * 2048)
}

// ParseSize parses a human size such as "64M", "512m", "2G" or a plain byte
// count. Suffixes K/M/G are powers of 1024; case-insensitive.
func ParseSize(s string) (int64, error) {
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

// dirBytes sums the sizes of regular files under root, for sizing content-sized
// images from a host directory.
func dirBytes(root string) (int64, error) {
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

// treeBytes sums the sizes of regular-file contents in a node tree.
func treeBytes(n *image.Node) int64 {
	var total int64
	for _, e := range n.Children {
		if e.Node.IsDir() {
			total += treeBytes(e.Node)
		} else if e.Node.Content != nil {
			total += e.Node.Content.Size()
		}
	}
	return total
}
