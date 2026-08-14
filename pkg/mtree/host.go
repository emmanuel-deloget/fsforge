package mtree

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// walkHost mirrors a host directory into nodes, carrying only what a
// specification records: type, mode, owner, time, symlink target and size.
// Contents are not read — a spec describes a tree, it does not hold one.
func walkHost(dir string, into *image.Node) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, de := range entries {
		fi, err := de.Info() // Lstat semantics: symlinks are not followed
		if err != nil {
			return err
		}
		n := &image.Node{Nlink: 1}
		n.Meta = tree.Meta{Mode: fi.Mode(), ModTime: fi.ModTime()}
		hostOwner(fi, n)

		switch {
		case fi.IsDir():
			n.Nlink = 2
			if err := into.AddChild(de.Name(), n); err != nil {
				return err
			}
			if err := walkHost(filepath.Join(dir, de.Name()), n); err != nil {
				return err
			}
			continue
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(filepath.Join(dir, de.Name()))
			if err != nil {
				return err
			}
			n.Link = target
		case fi.Mode().IsRegular():
			n.Content = sizeOnly(fi.Size())
		}
		if err := into.AddChild(de.Name(), n); err != nil {
			return err
		}
	}
	return nil
}

// sizeOnly is a tree.Source that knows a size and holds no bytes, which is all
// a specification needs to record.
type sizeOnly int64

func (s sizeOnly) Size() int64                           { return int64(s) }
func (sizeOnly) ReadAt(p []byte, off int64) (int, error) { return 0, io.EOF }
