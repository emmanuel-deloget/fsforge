package oci

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"io"
	"io/fs"
	"path"
	"sort"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// writeDiffLayer serialises the delta from oldRoot to newRoot as a tar layer:
// added or changed paths become normal entries, and paths present in oldRoot but
// absent from newRoot become overlay whiteouts (".wh.<name>"). Applied on top of
// the lower layers it reproduces newRoot exactly.
func writeDiffLayer(l *Layout, oldRoot, newRoot *image.Node, useGzip bool) (Descriptor, string, error) {
	return streamLayer(l, useGzip, func(tw *tar.Writer) error {
		return diffDir(tw, oldRoot, newRoot, "", map[*image.Node]string{})
	})
}

// diffDir emits the tar entries that turn the directory oldDir into newDir at
// path prefix. Either side may be nil (a wholly added or removed directory).
func diffDir(tw *tar.Writer, oldDir, newDir *image.Node, prefix string, seen map[*image.Node]string) error {
	oldM, newM := childMap(oldDir), childMap(newDir)
	for _, name := range unionNames(oldM, newM) {
		o, n := oldM[name], newM[name]
		full := path.Join(prefix, name)
		switch {
		case n == nil:
			// Present below, gone now: mask it with a whiteout.
			if err := writeWhiteout(tw, full); err != nil {
				return err
			}
		case o == nil:
			// New path: emit the whole subtree.
			if err := writeSubtree(tw, n, full, seen); err != nil {
				return err
			}
		case o.IsDir() && n.IsDir():
			// Both directories: re-emit the directory entry only if its own
			// metadata changed, then recurse.
			if ch, err := changed(o, n); err != nil {
				return err
			} else if ch {
				if err := writeEntry(tw, n, full, seen); err != nil {
					return err
				}
			}
			if err := diffDir(tw, o, n, full, seen); err != nil {
				return err
			}
		default:
			// At least one side is not a directory. If anything changed, emit the
			// new node (and its subtree, if it is now a directory). The upper
			// entry masks whatever the lower layer had at this path.
			ch, err := changed(o, n)
			if err != nil {
				return err
			}
			if ch {
				if err := writeSubtree(tw, n, full, seen); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// writeSubtree writes n at name and, when n is a directory, every descendant.
func writeSubtree(tw *tar.Writer, n *image.Node, name string, seen map[*image.Node]string) error {
	if err := writeEntry(tw, n, name, seen); err != nil {
		return err
	}
	if n.IsDir() {
		for _, e := range sortedChildren(n) {
			if err := writeSubtree(tw, e.Node, path.Join(name, e.Name), seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeWhiteout emits an overlay whiteout marking full as deleted in this layer.
func writeWhiteout(tw *tar.Writer, full string) error {
	dir, base := path.Split(full)
	return tw.WriteHeader(&tar.Header{
		Name:     path.Join(dir, whiteoutPrefix+base),
		Typeflag: tar.TypeReg,
		Mode:     0,
		Size:     0,
	})
}

// changed reports whether the node n differs from o in any way a layer must
// record: type, permissions, ownership, link/device target, xattrs, or — for
// regular files — size and content (compared by hashing, so equal-sized but
// different files are detected).
func changed(o, n *image.Node) (bool, error) {
	if o.Mode != n.Mode || o.UID != n.UID || o.GID != n.GID {
		return true, nil
	}
	if !xattrsEqual(o.Xattrs, n.Xattrs) {
		return true, nil
	}
	switch {
	case n.Mode&fs.ModeSymlink != 0:
		return o.Link != n.Link, nil
	case n.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		return o.Rdev != n.Rdev, nil
	case n.Mode.IsRegular():
		return regularChanged(o.Content, n.Content)
	default:
		return false, nil
	}
}

func regularChanged(o, n tree.Source) (bool, error) {
	os, ns := sourceSize(o), sourceSize(n)
	if os != ns {
		return true, nil
	}
	if ns == 0 {
		return false, nil
	}
	oh, err := hashSource(o)
	if err != nil {
		return false, err
	}
	nh, err := hashSource(n)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(oh, nh), nil
}

func sourceSize(s tree.Source) int64 {
	if s == nil {
		return 0
	}
	return s.Size()
}

func hashSource(s tree.Source) ([]byte, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(s, 0, s.Size())); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func xattrsEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		if !bytes.Equal(va, b[k]) {
			return false
		}
	}
	return true
}

// childMap indexes a directory's children by name; nil for a nil/childless node.
func childMap(n *image.Node) map[string]*image.Node {
	m := map[string]*image.Node{}
	if n == nil {
		return m
	}
	for _, e := range n.Children {
		m[e.Name] = e.Node
	}
	return m
}

// unionNames returns the sorted union of two child-name sets, so layer entries
// come out in a deterministic order.
func unionNames(a, b map[string]*image.Node) []string {
	set := map[string]struct{}{}
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
