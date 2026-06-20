package oci

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

const (
	whiteoutPrefix = ".wh."
	whiteoutOpaque = ".wh..wh..opq"
)

// Flatten applies the layers of the layout's first manifest (or the one tagged
// ref, if non-empty) in order into a single tree, honouring overlay whiteouts.
// Regular-file contents are extracted to a scratch directory and exposed as
// lazy sources, so memory stays bounded; call cleanup when done with the tree.
func Flatten(l *Layout, ref string, deps image.Deps) (img *image.Mem, cfg Image, cleanup func(), err error) {
	noop := func() {}
	man, cfg, err := l.resolve(ref)
	if err != nil {
		return nil, cfg, noop, err
	}
	scratch, err := os.MkdirTemp("", "fsforge-oci-*")
	if err != nil {
		return nil, cfg, noop, err
	}
	cleanup = func() { os.RemoveAll(scratch) }

	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	a := &applier{root: root, scratch: scratch}
	for _, layer := range man.Layers {
		if err := a.applyLayer(l, layer); err != nil {
			cleanup()
			return nil, cfg, noop, err
		}
	}
	return image.Adopt(deps, root), cfg, cleanup, nil
}

// resolve returns the manifest and config for ref (or the first manifest).
func (l *Layout) resolve(ref string) (Manifest, Image, error) {
	var man Manifest
	var cfg Image
	idx, err := l.Index()
	if err != nil {
		return man, cfg, err
	}
	if len(idx.Manifests) == 0 {
		return man, cfg, errors.New("oci: empty index")
	}
	chosen := idx.Manifests[0]
	if ref != "" {
		found := false
		for _, m := range idx.Manifests {
			if m.Annotations[annotationRefName] == ref {
				chosen, found = m, true
				break
			}
		}
		if !found {
			return man, cfg, errors.New("oci: ref not found: " + ref)
		}
	}
	if chosen.MediaType == MediaTypeIndex {
		return man, cfg, errors.New("oci: nested index not supported (single-platform only)")
	}
	if err := l.BlobJSON(chosen.Digest, &man); err != nil {
		return man, cfg, err
	}
	if err := l.BlobJSON(man.Config.Digest, &cfg); err != nil {
		return man, cfg, err
	}
	return man, cfg, nil
}

type applier struct {
	root    *image.Node
	scratch string
	fileSeq int
}

func (a *applier) applyLayer(l *Layout, desc Descriptor) error {
	rc, err := l.BlobReader(desc.Digest)
	if err != nil {
		return err
	}
	defer rc.Close()

	var src io.Reader = rc
	if strings.HasSuffix(desc.MediaType, "+gzip") {
		gz, err := gzip.NewReader(rc)
		if err != nil {
			return err
		}
		defer gz.Close()
		src = gz
	}

	tr := tar.NewReader(src)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := a.applyEntry(hdr, tr); err != nil {
			return err
		}
	}
}

func (a *applier) applyEntry(hdr *tar.Header, tr io.Reader) error {
	name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
	if name == "." || name == "/" || name == "" {
		return nil
	}
	dir, base := path.Split(name)
	parent := a.ensureDir(strings.TrimSuffix(dir, "/"))

	// Whiteouts delete from lower layers.
	if base == whiteoutOpaque {
		parent.Children = nil
		return nil
	}
	if strings.HasPrefix(base, whiteoutPrefix) {
		removeChild(parent, strings.TrimPrefix(base, whiteoutPrefix))
		return nil
	}

	meta := tree.Meta{
		Mode:    modeFromTar(hdr),
		UID:     uint32(hdr.Uid),
		GID:     uint32(hdr.Gid),
		ModTime: hdr.ModTime,
		Xattrs:  xattrsFromTar(hdr),
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		d := upsert(parent, base, meta)
		if !d.IsDir() {
			d.Mode = (d.Mode &^ fs.ModeType) | fs.ModeDir
		}
		if d.Nlink < 2 {
			d.Nlink = 2
		}
	case tar.TypeSymlink:
		n := upsert(parent, base, meta)
		n.Mode = (n.Mode &^ fs.ModeType) | fs.ModeSymlink
		n.Link = hdr.Linkname
	case tar.TypeLink:
		target := a.lookup(path.Clean(strings.TrimPrefix(hdr.Linkname, "./")))
		if target == nil {
			return errors.New("oci: hard link to missing target: " + hdr.Linkname)
		}
		target.Nlink++
		setChild(parent, base, target)
	case tar.TypeChar, tar.TypeBlock:
		n := upsert(parent, base, meta)
		t := fs.ModeDevice
		if hdr.Typeflag == tar.TypeChar {
			t = fs.ModeCharDevice | fs.ModeDevice
		}
		n.Mode = (n.Mode &^ fs.ModeType) | t
		n.Rdev = uint64(hdr.Devmajor)<<8 | uint64(hdr.Devminor)
	case tar.TypeFifo:
		n := upsert(parent, base, meta)
		n.Mode = (n.Mode &^ fs.ModeType) | fs.ModeNamedPipe
	case tar.TypeReg, tar.TypeRegA:
		src, err := a.spill(tr)
		if err != nil {
			return err
		}
		n := upsert(parent, base, meta)
		n.Mode = n.Mode &^ fs.ModeType // regular
		n.Content = src
		n.Nlink = 1
	}
	return nil
}

// spill writes a tar entry's bytes to a scratch file and returns a lazy source.
func (a *applier) spill(tr io.Reader) (tree.Source, error) {
	a.fileSeq++
	f, err := os.CreateTemp(a.scratch, "blob-*")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n, err := io.Copy(f, tr)
	if err != nil {
		return nil, err
	}
	return &fileSource{path: f.Name(), size: n}, nil
}

// ensureDir walks/creates the directory chain for p (relative, slash-separated).
func (a *applier) ensureDir(p string) *image.Node {
	cur := a.root
	if p == "" || p == "." {
		return cur
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" {
			continue
		}
		child := findChild(cur, part)
		if child == nil || !child.IsDir() {
			child = &image.Node{
				Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}},
				Nlink: 2,
			}
			setChild(cur, part, child)
		}
		cur = child
	}
	return cur
}

func (a *applier) lookup(p string) *image.Node {
	cur := a.root
	for _, part := range strings.Split(p, "/") {
		if part == "" {
			continue
		}
		cur = findChild(cur, part)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// --- node helpers operating directly on Children ---

func findChild(n *image.Node, name string) *image.Node {
	for i := range n.Children {
		if n.Children[i].Name == name {
			return n.Children[i].Node
		}
	}
	return nil
}

func setChild(parent *image.Node, name string, n *image.Node) {
	for i := range parent.Children {
		if parent.Children[i].Name == name {
			parent.Children[i].Node = n
			return
		}
	}
	parent.Children = append(parent.Children, image.Entry{Name: name, Node: n})
}

func removeChild(parent *image.Node, name string) {
	for i := range parent.Children {
		if parent.Children[i].Name == name {
			parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
			return
		}
	}
}

// upsert returns the existing child to overwrite in place, or a fresh node set
// under name, applying meta either way.
func upsert(parent *image.Node, name string, meta tree.Meta) *image.Node {
	if n := findChild(parent, name); n != nil {
		n.Meta = meta
		return n
	}
	n := &image.Node{Inode: tree.Inode{Meta: meta}, Nlink: 1}
	parent.Children = append(parent.Children, image.Entry{Name: name, Node: n})
	return n
}

func modeFromTar(hdr *tar.Header) fs.FileMode {
	m := fs.FileMode(hdr.Mode & 0o777)
	if hdr.Mode&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if hdr.Mode&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if hdr.Mode&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m
}

func xattrsFromTar(hdr *tar.Header) map[string][]byte {
	var x map[string][]byte
	for k, v := range hdr.PAXRecords {
		if strings.HasPrefix(k, "SCHILY.xattr.") {
			if x == nil {
				x = map[string][]byte{}
			}
			x[strings.TrimPrefix(k, "SCHILY.xattr.")] = []byte(v)
		}
	}
	return x
}

// fileSource is a lazy tree.Source backed by a scratch file.
type fileSource struct {
	path string
	size int64
	f    *os.File
}

func (s *fileSource) Size() int64 { return s.size }

func (s *fileSource) ReadAt(p []byte, off int64) (int, error) {
	if s.f == nil {
		f, err := os.Open(s.path)
		if err != nil {
			return 0, err
		}
		s.f = f
	}
	return s.f.ReadAt(p, off)
}
