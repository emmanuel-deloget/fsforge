package mtree

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Apply lays a specification over a tree.
//
// An entry that names an existing node amends it: only the keywords present in
// the spec are changed, so a spec that says nothing but "uid=0 gid=0" leaves
// modes and contents alone. An entry that names a node that is not there
// creates it, which is what a checkout cannot do for itself — device nodes,
// fifos, and directories that exist only in the image.
//
// Parent directories are created as needed, so a spec can name /dev/console
// without naming /dev first. They are created with 0755 and uid/gid 0, which a
// later entry for the directory itself is free to override.
//
// The returned io.Closer owns any host files opened for contents= keywords and
// must be closed after the image is finalized, not before: contents are
// streamed, never buffered.
func Apply(root *image.Node, spec *Spec) (closer, error) {
	mc := &multiCloser{}
	for _, e := range spec.Entries {
		if err := applyEntry(root, e, mc); err != nil {
			mc.Close()
			return mc, err
		}
	}
	return mc, nil
}

func applyEntry(root *image.Node, e Entry, mc *multiCloser) error {
	if e.Path == "" { // the root itself
		return amend(root, e, mc)
	}
	parts := strings.Split(e.Path, "/")
	parent := root
	for _, part := range parts[:len(parts)-1] {
		next, err := childDir(parent, part, e.Line)
		if err != nil {
			return err
		}
		parent = next
	}

	name := parts[len(parts)-1]
	if err := image.ValidName(name); err != nil {
		return errAt(e.Line, "%v", err)
	}
	n := findChild(parent, name)
	if n == nil {
		n = &image.Node{Nlink: 1}
		if e.Type != nil && *e.Type == TypeDir {
			// A directory starts at two — itself and the entry about to be added —
			// and its ".." adds one to the parent. Skipping that second half
			// produces a tree fsck rejects: "ref count is 4, should be 7".
			n.Nlink = 2
			parent.Nlink++
		}
		n.Mode = defaultModeFor(e)
		if err := parent.AddChild(name, n); err != nil {
			return errAt(e.Line, "%v", err)
		}
	}
	return amend(n, e, mc)
}

// defaultModeFor is the mode a created node starts with, before the spec's own
// keywords are applied. A spec that names a device without a mode gets 0600
// rather than 0000, which would be a node nothing can open.
func defaultModeFor(e Entry) fs.FileMode {
	t := TypeFile
	if e.Type != nil {
		t = *e.Type
	}
	perm := fs.FileMode(0o644)
	switch t {
	case TypeDir:
		perm = 0o755
	case TypeLink:
		perm = 0o777
	case TypeBlock, TypeChar, TypeFifo, TypeSocket:
		perm = 0o600
	}
	return t.mode() | perm
}

// amend applies the keywords that were set, leaving the rest of the node alone.
func amend(n *image.Node, e Entry, mc *multiCloser) error {
	if e.Type != nil {
		want := e.Type.mode()
		if n.Mode&fs.ModeType != want && n.Children != nil {
			return errAt(e.Line, "%s: cannot change a non-empty directory into %s", e.Path, *e.Type)
		}
		n.Mode = (n.Mode &^ fs.ModeType) | want
	}
	if e.Mode != nil {
		n.Mode = (n.Mode & fs.ModeType) | *e.Mode
	}
	if e.UID != nil {
		n.UID = *e.UID
	}
	if e.GID != nil {
		n.GID = *e.GID
	}
	if e.Time != nil {
		n.ModTime = *e.Time
	}
	if e.Link != nil {
		n.Link = *e.Link
	}
	if e.Major != nil && e.Minor != nil {
		n.Rdev = uint64(*e.Major)<<8 | uint64(*e.Minor)
	}
	for k, v := range e.Xattrs {
		if n.Xattrs == nil {
			n.Xattrs = map[string][]byte{}
		}
		n.Xattrs[k] = v
	}
	if e.Contents != nil {
		f, err := os.Open(*e.Contents)
		if err != nil {
			return errAt(e.Line, "contents: %v", err)
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return errAt(e.Line, "contents: %v", err)
		}
		mc.add(f)
		n.Content = &fileSource{f: f, size: st.Size()}
	}
	// A regular file with neither contents nor a source of its own is empty
	// rather than nil, so engines have something to lay out.
	if n.Mode&fs.ModeType == 0 && n.Content == nil {
		n.Content = tree.Bytes(nil)
	}
	return nil
}

// childDir walks to a subdirectory, creating it if the spec skipped over it.
func childDir(parent *image.Node, name string, line int) (*image.Node, error) {
	if err := image.ValidName(name); err != nil {
		return nil, errAt(line, "%v", err)
	}
	if n := findChild(parent, name); n != nil {
		if !n.IsDir() {
			return nil, errAt(line, "%s is not a directory", name)
		}
		return n, nil
	}
	n := &image.Node{
		Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}},
		Nlink: 2,
	}
	parent.Nlink++ // the new directory's ".." points back here
	if err := parent.AddChild(name, n); err != nil {
		return nil, errAt(line, "%v", err)
	}
	return n, nil
}

func findChild(n *image.Node, name string) *image.Node {
	for i := range n.Children {
		if n.Children[i].Name == name {
			return n.Children[i].Node
		}
	}
	return nil
}

// fileSource streams a host file as a node's contents.
type fileSource struct {
	f    *os.File
	size int64
}

func (s *fileSource) Size() int64                             { return s.size }
func (s *fileSource) ReadAt(p []byte, off int64) (int, error) { return s.f.ReadAt(p, off) }

// closer is what Apply hands back; it is io.Closer, named so the package's
// documentation does not have to import io just to say so.
type closer interface{ Close() error }

type multiCloser struct{ cs []*os.File }

func (m *multiCloser) add(f *os.File) { m.cs = append(m.cs, f) }

func (m *multiCloser) Close() error {
	var first error
	for _, c := range m.cs {
		if err := c.Close(); err != nil && first == nil {
			first = fmt.Errorf("mtree: %w", err)
		}
	}
	return first
}
