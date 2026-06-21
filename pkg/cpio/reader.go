package cpio

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Open parses an existing newc cpio archive into the agnostic tree, so cpio can
// be a conversion source. Hard-linked regular files (shared inode) are folded
// back into a single shared node. The returned image is read-only: an archive
// is rewritten rather than edited in place, so rebuild via Convert.
func (e *Cpio) Open(dev device.Device) (image.Image, error) {
	r := &creader{dev: dev}
	root, err := r.read()
	if err != nil {
		return nil, err
	}
	return &cImageRead{Mem: image.Adopt(e.deps, root)}, nil
}

// cImageRead is an opened (read-only) cpio archive.
type cImageRead struct{ *image.Mem }

func (cImageRead) Finalize() error {
	return errors.New("cpio: cannot re-finalize an opened archive; rebuild via convert")
}

type creader struct {
	dev device.Device
}

func (r *creader) read() (*image.Node, error) {
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	inoMap := map[uint32]*image.Node{}

	var pos int64
	for {
		head := make([]byte, headerSize)
		if _, err := r.dev.ReadAt(head, pos); err != nil && err != io.EOF {
			return nil, err
		}
		h, ok, err := parseHeader(head)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("cpio: bad or missing newc magic")
		}

		nameBuf := make([]byte, h.namesize)
		if _, err := r.dev.ReadAt(nameBuf, pos+headerSize); err != nil && err != io.EOF {
			return nil, err
		}
		name := trimNUL(nameBuf)
		if name == trailerName {
			break
		}

		bodyStart := pos + headerSize + int64(nAlign(int(h.namesize)))
		if err := r.addEntry(root, h, name, bodyStart, inoMap); err != nil {
			return nil, err
		}
		pos = align4(bodyStart + int64(h.filesize))
	}
	return root, nil
}

func (r *creader) addEntry(root *image.Node, h hdr, name string, bodyStart int64, inoMap map[uint32]*image.Node) error {
	comps := splitPath(name)
	if len(comps) == 0 {
		return nil // "." or empty: the root itself
	}
	mode := modeFromUnix(h.mode)

	// Hard-linked regular files share one node keyed by inode number.
	isReg := mode&fs.ModeType == 0
	if isReg && h.nlink >= 2 {
		if existing, ok := inoMap[h.ino]; ok {
			if h.filesize > 0 { // the body rides one of the links
				existing.Content = &cpioFile{dev: r.dev, off: bodyStart, size: int64(h.filesize)}
			}
			existing.Nlink++
			return link(root, comps, existing)
		}
	}

	n := &image.Node{Nlink: int(h.nlink)}
	n.Meta = tree.Meta{
		Mode:    mode,
		UID:     h.uid,
		GID:     h.gid,
		ModTime: time.Unix(h.mtime, 0).UTC(),
	}
	switch {
	case mode.IsDir():
		n.Nlink = int(h.nlink)
	case mode&fs.ModeSymlink != 0:
		buf := make([]byte, h.filesize)
		if _, err := r.dev.ReadAt(buf, bodyStart); err != nil && err != io.EOF {
			return err
		}
		n.Link = string(buf)
	case mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		n.Rdev = uint64(h.rdevmajor)<<8 | uint64(h.rdevminor)
	default: // regular file
		if h.filesize > 0 {
			n.Content = &cpioFile{dev: r.dev, off: bodyStart, size: int64(h.filesize)}
		} else {
			n.Content = tree.Bytes(nil)
		}
		if isReg && h.nlink >= 2 {
			inoMap[h.ino] = n
		}
	}
	return link(root, comps, n)
}

// link attaches node at comps within root, creating intermediate directories
// and reconciling a placeholder directory created earlier for a deeper entry.
func link(root *image.Node, comps []string, node *image.Node) error {
	dir := root
	for _, c := range comps[:len(comps)-1] {
		dir = childDir(dir, c)
	}
	leaf := comps[len(comps)-1]
	for i := range dir.Children {
		if dir.Children[i].Name == leaf {
			// A directory placeholder may already exist; adopt the real metadata.
			if node.IsDir() && dir.Children[i].Node.IsDir() {
				existing := dir.Children[i].Node
				existing.Meta = node.Meta
				existing.Nlink = node.Nlink
				return nil
			}
			dir.Children[i].Node = node
			return nil
		}
	}
	dir.Children = append(dir.Children, image.Entry{Name: leaf, Node: node})
	return nil
}

// childDir returns the child directory named c under dir, creating a
// placeholder for archives that mention a file before its parent directory.
func childDir(dir *image.Node, c string) *image.Node {
	for i := range dir.Children {
		if dir.Children[i].Name == c {
			return dir.Children[i].Node
		}
	}
	child := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	dir.Children = append(dir.Children, image.Entry{Name: c, Node: child})
	return child
}

// splitPath cleans an archive path into components, dropping "." and empty
// parts (so "./etc/hosts" and "etc/hosts" agree, and the root maps to none).
func splitPath(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." {
			continue
		}
		out = append(out, c)
	}
	return out
}

func trimNUL(b []byte) string {
	if i := indexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// cpioFile is a lazy tree.Source over a regular file's body in the archive.
type cpioFile struct {
	dev  device.Device
	off  int64
	size int64
}

func (f *cpioFile) Size() int64 { return f.size }

func (f *cpioFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("cpio: negative offset")
	}
	if off >= f.size {
		return 0, io.EOF
	}
	if rem := f.size - off; int64(len(p)) > rem {
		n, err := f.dev.ReadAt(p[:rem], f.off+off)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return f.dev.ReadAt(p, f.off+off)
}
