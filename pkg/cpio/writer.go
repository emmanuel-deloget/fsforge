package cpio

import (
	"io"
	"io/fs"
	"sort"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// cwriter streams a node tree into a newc cpio archive. Entries are emitted in
// a deterministic pre-order walk (a directory before its contents) with the
// root left implicit, exactly as gen_init_cpio lays out an initramfs.
type cwriter struct {
	dev   device.Device
	clock image.Clock
	pos   int64
	err   error
}

func newCwriter(dev device.Device, clock image.Clock) *cwriter {
	return &cwriter{dev: dev, clock: clock}
}

// pathEntry is one archive member: its archive path and the node.
type pathEntry struct {
	path string
	node *image.Node
}

func (w *cwriter) writeArchive(root *image.Node) {
	var entries []pathEntry
	collect(root, "", &entries)

	// Assign a unique inode number per node and find each node's last entry, so
	// hard-linked regular files share an inode and the body rides the last link.
	ino := map[*image.Node]uint32{}
	occ := map[*image.Node]int{}
	last := map[*image.Node]int{}
	var next uint32 = 1
	for i, e := range entries {
		if _, ok := ino[e.node]; !ok {
			ino[e.node] = next
			next++
		}
		occ[e.node]++
		last[e.node] = i
	}

	for i, e := range entries {
		w.writeEntry(e.path, e.node, ino[e.node], uint32(occ[e.node]), last[e.node] == i)
	}
	w.writeTrailer()
	w.padArchive()
}

// collect walks the tree pre-order, appending every node but the root. The root
// maps onto the extraction directory itself and is left implicit.
func collect(n *image.Node, prefix string, out *[]pathEntry) {
	for _, e := range sortChildren(n) {
		path := e.Name
		if prefix != "" {
			path = prefix + "/" + e.Name
		}
		*out = append(*out, pathEntry{path: path, node: e.Node})
		if e.Node.IsDir() {
			collect(e.Node, path, out)
		}
	}
}

// writeEntry emits one archive member. withBody is true on the entry that
// carries a regular file's contents (the last of a hard-link set); other links
// carry an empty body and the kernel links them by shared inode.
func (w *cwriter) writeEntry(path string, n *image.Node, ino, nlink uint32, withBody bool) {
	isReg := !n.IsDir() && n.Mode&(fs.ModeSymlink|fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) == 0

	var body []byte     // small inline body (symlink target)
	var filesize uint32 // declared body length
	switch {
	case n.Mode&fs.ModeSymlink != 0:
		body = []byte(n.Link)
		filesize = uint32(len(body))
	case isReg && withBody && n.Content != nil:
		filesize = uint32(n.Content.Size())
	}

	mt := n.ModTime
	if mt.IsZero() {
		mt = w.clock.Now()
	}
	h := hdr{
		ino:      ino,
		mode:     modeToUnix(n.Mode),
		uid:      n.UID,
		gid:      n.GID,
		nlink:    nlink,
		mtime:    mt.Unix(),
		filesize: filesize,
		namesize: uint32(len(path) + 1),
	}
	if n.IsDir() {
		h.nlink = uint32(n.Nlink)
	}
	if n.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0 {
		h.rdevmajor = uint32(n.Rdev >> 8)
		h.rdevminor = uint32(n.Rdev & 0xff)
	}

	w.write(h.marshal())
	w.writeName(path)

	switch {
	case body != nil:
		w.write(body)
		w.pad4()
	case isReg && withBody && n.Content != nil:
		w.streamContent(n.Content, int64(filesize))
		w.pad4()
	}
}

func (w *cwriter) writeName(path string) {
	name := make([]byte, len(path)+1) // trailing NUL
	copy(name, path)
	w.write(name)
	w.writePad(nAlign(len(name)) - len(name))
}

func (w *cwriter) streamContent(src io.ReaderAt, size int64) {
	buf := make([]byte, 64<<10)
	for off := int64(0); off < size; {
		nb := int64(len(buf))
		if rem := size - off; rem < nb {
			nb = rem
		}
		n, err := src.ReadAt(buf[:nb], off)
		if n > 0 {
			w.write(buf[:n])
			off += int64(n)
		}
		if err != nil && err != io.EOF {
			w.err = err
			return
		}
		if n == 0 {
			return
		}
	}
}

func (w *cwriter) writeTrailer() {
	h := hdr{nlink: 1, namesize: uint32(len(trailerName) + 1)}
	w.write(h.marshal())
	w.writeName(trailerName)
}

// padArchive zero-pads the archive up to a 512-byte boundary, matching GNU cpio.
func (w *cwriter) padArchive() {
	if rem := w.pos % padTo; rem != 0 {
		w.writePad(int(padTo - rem))
	}
}

func (w *cwriter) pad4() {
	if rem := w.pos & 3; rem != 0 {
		w.writePad(int(4 - rem))
	}
}

func (w *cwriter) writePad(n int) {
	if n > 0 {
		w.write(make([]byte, n))
	}
}

func (w *cwriter) write(p []byte) {
	if w.err != nil {
		return
	}
	if _, err := w.dev.WriteAt(p, w.pos); err != nil {
		w.err = err
		return
	}
	w.pos += int64(len(p))
}

func sortChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
