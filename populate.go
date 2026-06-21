package fsforge

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// PopulateFromDir mirrors the host directory srcDir into the image directory
// dir, recursively, preserving regular files, subdirectories and symlinks.
//
// File contents are streamed at finalize time, not buffered: each regular file
// is added as a lazy tree.Source backed by an open *os.File. The returned
// io.Closer owns those handles and must be closed *after* the image is
// finalized — closing it earlier would cut the content streams short.
func PopulateFromDir(dir image.Dir, srcDir string) (io.Closer, error) {
	mc := &multiCloser{}
	if err := populate(dir, srcDir, mc); err != nil {
		return mc, err
	}
	return mc, nil
}

func populate(dir image.Dir, src string, mc *multiCloser) error {
	entries, err := os.ReadDir(src) // sorted by name
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(src, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		m := tree.Meta{Mode: info.Mode(), ModTime: info.ModTime()}
		switch {
		case e.IsDir():
			sub, err := dir.Mkdir(e.Name(), m)
			if err != nil {
				return err
			}
			if err := populate(sub, full, mc); err != nil {
				return err
			}
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				return err
			}
			if err := dir.Symlink(e.Name(), target, m); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			f, err := os.Open(full)
			if err != nil {
				return err
			}
			mc.add(f)
			if _, err := dir.Create(e.Name(), &osSource{f: f, size: info.Size()}, m); err != nil {
				return err
			}
		}
	}
	return nil
}

// Graft recreates the children of the source node tree under dstDir using the
// editing API, reusing lazy content sources (no buffering) and preserving hard
// links. It is the tree-to-tree counterpart of PopulateFromDir, used when the
// source is already an in-memory tree (e.g. another image or an OCI flatten).
func Graft(dstDir image.Dir, src *image.Node) error {
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

// ExtractToDir writes the node tree to a host directory: regular files,
// subdirectories and symlinks. Special files (devices, fifos, sockets) need
// privileges and are skipped.
func ExtractToDir(root *image.Node, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	for _, e := range sortedEntries(root) {
		if err := extractOne(filepath.Join(dstDir, e.Name), e.Node); err != nil {
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
			if _, err := f.ReadFrom(io.NewSectionReader(n.Content, 0, n.Content.Size())); err != nil {
				return err
			}
		}
	}
	return nil
}

// sortedEntries returns a node's children ordered by name, so directory
// layouts stay deterministic regardless of the source's insertion order.
func sortedEntries(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// osSource is a lazy tree.Source over a host file.
type osSource struct {
	f    *os.File
	size int64
}

func (s *osSource) Size() int64                             { return s.size }
func (s *osSource) ReadAt(p []byte, off int64) (int, error) { return s.f.ReadAt(p, off) }

// multiCloser closes a set of handles, reporting the first error.
type multiCloser struct{ cs []io.Closer }

func (m *multiCloser) add(c io.Closer) { m.cs = append(m.cs, c) }

func (m *multiCloser) Close() error {
	var first error
	for _, c := range m.cs {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
