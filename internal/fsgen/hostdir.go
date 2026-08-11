package fsgen

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/emmanuel-deloget/fsforge/internal/manifest"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// WriteDir materialises a generated tree on the host, so an external tool can
// be pointed at it and its output read back with fsforge — the other direction
// of the differential test.
//
// What an unprivileged process cannot create is skipped rather than faked:
// device nodes need CAP_MKNOD, and owners cannot be set at all. Callers get the
// manifest of what was actually written, so the comparison is against reality
// rather than against what the generator intended.
func WriteDir(root *image.Node, dst string) (manifest.Manifest, error) {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	if err := writeChildren(root, dst); err != nil {
		return nil, err
	}
	// Directory modes are applied on the way out: a directory created read-only
	// cannot then be written into.
	if err := applyDirModes(root, dst); err != nil {
		return nil, err
	}
	return manifest.FromDir(dst)
}

func writeChildren(n *image.Node, dir string) error {
	for _, e := range n.Children {
		p := filepath.Join(dir, e.Name)
		child := e.Node
		switch {
		case child.IsDir():
			if err := os.Mkdir(p, 0o755); err != nil {
				return err
			}
			if err := writeChildren(child, p); err != nil {
				return err
			}
		case child.Mode&fs.ModeSymlink != 0:
			if err := os.Symlink(child.Link, p); err != nil {
				return err
			}
		case child.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
			continue // needs privileges; the generator is told not to emit these
		default:
			if err := writeFile(p, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeFile(p string, n *image.Node) error {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, n.Mode.Perm())
	if err != nil {
		return err
	}
	defer f.Close()
	if n.Content == nil || n.Content.Size() == 0 {
		return nil
	}
	buf := make([]byte, 64<<10)
	for off := int64(0); off < n.Content.Size(); {
		end := int64(len(buf))
		if rem := n.Content.Size() - off; rem < end {
			end = rem
		}
		if _, err := n.Content.ReadAt(buf[:end], off); err != nil {
			return err
		}
		if _, err := f.Write(buf[:end]); err != nil {
			return err
		}
		off += end
	}
	return f.Chmod(n.Mode.Perm())
}

func applyDirModes(n *image.Node, dir string) error {
	for _, e := range n.Children {
		if !e.Node.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name)
		if err := applyDirModes(e.Node, p); err != nil {
			return err
		}
		if err := os.Chmod(p, e.Node.Mode.Perm()); err != nil {
			return err
		}
	}
	return nil
}
