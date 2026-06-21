package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/ext"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/oci"
	"github.com/emmanuel-deloget/fsforge/pkg/squashfs"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// convert wires any supported source to any supported sink through the shared
// tree model: dir/ext2/ext4/oci → tree → dir/ext2/ext4/squashfs/oci.
func convert(args []string) error {
	fsSet := flag.NewFlagSet("convert", flag.ContinueOnError)
	from := fsSet.String("from", "", "source as <kind>:<path> (dir, ext2, ext4, oci)")
	to := fsSet.String("to", "", "sink as <kind>:<path> (dir, ext2, ext4, squashfs, oci)")
	sizeStr := fsSet.String("size", "", "image size for ext sinks, e.g. 512M")
	blockSize := fsSet.Uint("block-size", 0, "block size in bytes")
	ref := fsSet.String("ref", "fsforge:latest", "image ref for oci sink")
	reproducible := fsSet.Bool("reproducible", false, "deterministic output")
	fsSet.Parse(args)
	if *from == "" || *to == "" {
		return fmt.Errorf("-from and -to are required")
	}

	srcKind, srcPath, err := parseLoc(*from)
	if err != nil {
		return err
	}
	dstKind, dstPath, err := parseLoc(*to)
	if err != nil {
		return err
	}
	deps := buildDeps(*reproducible)

	root, cfg, cleanup, err := loadTree(srcKind, srcPath, deps)
	if err != nil {
		return fmt.Errorf("load %s: %w", *from, err)
	}
	defer cleanup()

	w := writeParams{
		kind: dstKind, path: dstPath, deps: deps,
		sizeStr: *sizeStr, blockSize: uint32(*blockSize), ref: *ref, cfg: cfg,
	}
	if err := writeTree(root, w); err != nil {
		return fmt.Errorf("write %s: %w", *to, err)
	}
	fmt.Printf("converted %s -> %s\n", *from, *to)
	return nil
}

func parseLoc(s string) (kind, path string, err error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", "", fmt.Errorf("expected <kind>:<path>, got %q", s)
	}
	return s[:i], s[i+1:], nil
}

// --- sources: produce a tree root ---

type rootNoder interface{ RootNode() *image.Node }

func loadTree(kind, path string, deps image.Deps) (*image.Node, *oci.Image, func(), error) {
	noop := func() {}
	switch kind {
	case "dir":
		mem := image.NewMem(deps, tree.Meta{Mode: fs.ModeDir | 0o755})
		closers, err := populate(mem.Root(), path)
		cleanup := func() {
			for _, c := range closers {
				c.Close()
			}
		}
		if err != nil {
			cleanup()
			return nil, nil, noop, err
		}
		return mem.RootNode(), nil, cleanup, nil

	case "ext2", "ext4":
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, noop, err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, nil, noop, err
		}
		eng := ext.NewExt2(deps) // Open recovers the variant from the superblock
		img, err := eng.Open(device.NewFile(f, info.Size()))
		if err != nil {
			f.Close()
			return nil, nil, noop, err
		}
		return img.(rootNoder).RootNode(), nil, func() { f.Close() }, nil

	case "squashfs":
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, noop, err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, nil, noop, err
		}
		img, err := squashfs.New(deps).Open(device.NewFile(f, info.Size()))
		if err != nil {
			f.Close()
			return nil, nil, noop, err
		}
		return img.(rootNoder).RootNode(), nil, func() { f.Close() }, nil

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

// --- sinks: consume a tree root ---

type writeParams struct {
	kind, path string
	deps       image.Deps
	sizeStr    string
	blockSize  uint32
	ref        string
	cfg        *oci.Image
}

func writeTree(root *image.Node, w writeParams) error {
	switch w.kind {
	case "dir":
		return extractDir(root, w.path)

	case "ext2", "ext4", "squashfs", "fat", "fat32", "exfat", "iso", "iso9660":
		eng, err := engineFor(w.kind, w.deps, w.blockSize)
		if err != nil {
			return err
		}
		size, err := sinkSize(w.kind, w.sizeStr, root, w.blockSize)
		if err != nil {
			return err
		}
		f, err := os.Create(w.path)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := f.Truncate(size); err != nil {
			return err
		}
		img, err := eng.Format(device.NewFile(f, size), image.Params{BlockSize: w.blockSize})
		if err != nil {
			return err
		}
		if err := graft(img.Root(), root); err != nil {
			return err
		}
		if err := img.Finalize(); err != nil {
			return err
		}
		switch w.kind {
		case "squashfs":
			return trimSquashfs(f)
		case "iso", "iso9660":
			return trimISO(f)
		}
		return nil

	case "oci":
		l, err := oci.CreateLayout(w.path)
		if err != nil {
			return err
		}
		mem := image.Adopt(w.deps, root)
		opt := oci.BuildOptions{Ref: w.ref, Gzip: true}
		if w.cfg != nil { // carry runtime config across an oci->oci conversion
			opt.Architecture = w.cfg.Architecture
			opt.OS = w.cfg.OS
			opt.Config = w.cfg.Config
		}
		_, err = oci.Build(l, mem, opt)
		return err

	default:
		return fmt.Errorf("unknown sink kind %q", w.kind)
	}
}

// graft recreates src's children under dstDir via the editing API, reusing lazy
// content sources (no buffering) and preserving hard links.
func graft(dstDir image.Dir, src *image.Node) error {
	seen := map[*image.Node]image.File{}
	for _, e := range sortedEntries(src) {
		if err := graftOne(dstDir, e.Name, e.Node, seen); err != nil {
			return err
		}
	}
	return nil
}

func graftOne(dstDir image.Dir, name string, n *image.Node, seen map[*image.Node]image.File) error {
	switch {
	case n.IsDir():
		sub, err := dstDir.Mkdir(name, n.Meta)
		if err != nil {
			return err
		}
		for _, e := range sortedEntries(n) {
			if err := graftOne(sub, e.Name, e.Node, seen); err != nil {
				return err
			}
		}
	case n.Mode&fs.ModeSymlink != 0:
		return dstDir.Symlink(name, n.Link, n.Meta)
	case n.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
		return dstDir.Mknod(name, n.Rdev, n.Meta)
	default:
		if h, ok := seen[n]; ok { // hard link to an already-created file
			return dstDir.Link(name, h)
		}
		h, err := dstDir.Create(name, n.Content, n.Meta)
		if err != nil {
			return err
		}
		if n.Nlink > 1 {
			seen[n] = h
		}
	}
	return nil
}

// extractDir writes the tree to a host directory (regular files, dirs, symlinks).
func extractDir(root *image.Node, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range sortedEntries(root) {
		if err := extractOne(filepath.Join(dst, e.Name), e.Node); err != nil {
			return err
		}
	}
	return nil
}

func extractOne(path string, n *image.Node) error {
	switch {
	case n.IsDir():
		if err := os.MkdirAll(path, n.Mode.Perm()); err != nil {
			return err
		}
		for _, e := range sortedEntries(n) {
			if err := extractOne(filepath.Join(path, e.Name), e.Node); err != nil {
				return err
			}
		}
	case n.Mode&fs.ModeSymlink != 0:
		return os.Symlink(n.Link, path)
	case n.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
		return nil // special files need privileges; skip on extract
	default:
		f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, n.Mode.Perm())
		if err != nil {
			return err
		}
		defer f.Close()
		if n.Content != nil && n.Content.Size() > 0 {
			if _, err := f.ReadFrom(sourceReader(n.Content)); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedEntries(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sinkSize chooses the backing size for an image sink. ext requires an explicit
// size; squashfs is sized from the tree and trimmed afterwards.
func sinkSize(kind, sizeStr string, root *image.Node, blockSize uint32) (int64, error) {
	if kind == "squashfs" || kind == "iso" || kind == "iso9660" {
		total := treeBytes(root)
		return total + total/2 + (16 << 20), nil
	}
	if sizeStr == "" {
		return 0, fmt.Errorf("-size is required for %s sink", kind)
	}
	size, err := parseSize(sizeStr)
	if err != nil {
		return 0, err
	}
	bs := int64(blockSize)
	if bs == 0 {
		if kind == "ext4" {
			bs = 4096
		} else {
			bs = 1024
		}
	}
	return size - size%bs, nil
}

func sourceReader(s tree.Source) *io.SectionReader { return io.NewSectionReader(s, 0, s.Size()) }

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
