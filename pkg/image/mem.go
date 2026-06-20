package image

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Node is one filesystem object in the in-memory build tree. It embeds the
// agnostic tree.Inode and adds the working state shared by all engines: the
// directory entries, the link count, and a slot for the inode number an engine
// assigns during layout. Sharing one *Node under several Entry names expresses
// a hard link.
type Node struct {
	tree.Inode
	Nlink    int
	Children []Entry // directory entries; meaningful only when IsDir is true
	Ino      uint32  // engine scratch: assigned during layout
}

// Entry binds a name to a child node within a directory.
type Entry struct {
	Name string
	Node *Node
}

// IsDir reports whether the node is a directory.
func (n *Node) IsDir() bool { return n.Mode.IsDir() }

// Mem is the generic, in-memory implementation of an editable image tree. An
// engine embeds *Mem to get the whole Dir editing surface for free and only has
// to supply Finalize (the layout pass). Because Open builds the same Mem from an
// existing image, create and offline-mutate share this one editing model.
type Mem struct {
	deps Deps
	root *Node
}

// NewMem returns an empty image whose root is a directory with the given
// metadata. The injected Clock is normalised so engines never see a nil clock.
func NewMem(deps Deps, rootMeta tree.Meta) *Mem {
	if deps.Clock == nil {
		deps.Clock = SystemClock{}
	}
	if deps.UUID == nil {
		deps.UUID = RandomUUID{}
	}
	rootMeta = resolveMeta(deps, rootMeta)
	rootMeta.Mode = (rootMeta.Mode &^ fs.ModeType) | fs.ModeDir
	return &Mem{
		deps: deps,
		root: &Node{Inode: tree.Inode{Meta: rootMeta}, Nlink: 2},
	}
}

// Adopt wraps an already-built node tree as an editable image. Engines use it in
// Open: an existing image is parsed into Nodes, then handed back through the
// same editing surface as a freshly formatted one.
func Adopt(deps Deps, root *Node) *Mem {
	if deps.Clock == nil {
		deps.Clock = SystemClock{}
	}
	if deps.UUID == nil {
		deps.UUID = RandomUUID{}
	}
	return &Mem{deps: deps, root: root}
}

// Deps returns the injected dependencies, for engines that need the clock,
// UUID source or allocator factory during Finalize.
func (m *Mem) Deps() Deps { return m.deps }

// RootNode returns the root build node, for engines to walk during layout.
func (m *Mem) RootNode() *Node { return m.root }

// Root returns the editable root directory.
func (m *Mem) Root() Dir { return &dirHandle{m: m, n: m.root} }

func resolveMeta(deps Deps, meta tree.Meta) tree.Meta {
	if meta.ModTime.IsZero() {
		meta.ModTime = deps.Clock.Now()
	}
	return meta
}

// dirHandle is the Dir implementation; n is the directory node it edits.
type dirHandle struct {
	m *Mem
	n *Node
}

// fileHandle is the File implementation, an opaque handle to a node used as the
// target of Link.
type fileHandle struct{ n *Node }

func (f *fileHandle) inode() *tree.Inode { return &f.n.Inode }

var (
	errBadName  = errors.New("image: invalid name")
	errNotDir   = errors.New("image: not a directory")
	errNotEmpty = errors.New("image: directory not empty")
	errBadLink  = errors.New("image: link target is not a regular file handle")
)

func checkName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return errBadName
	}
	return nil
}

func (d *dirHandle) find(name string) *Entry {
	for i := range d.n.Children {
		if d.n.Children[i].Name == name {
			return &d.n.Children[i]
		}
	}
	return nil
}

func (d *dirHandle) add(name string, n *Node) {
	d.n.Children = append(d.n.Children, Entry{Name: name, Node: n})
}

func (d *dirHandle) newChild(name string, meta tree.Meta, typeBits fs.FileMode) (*Node, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	if d.find(name) != nil {
		return nil, fs.ErrExist
	}
	meta = resolveMeta(d.m.deps, meta)
	meta.Mode = (meta.Mode &^ fs.ModeType) | typeBits
	return &Node{Inode: tree.Inode{Meta: meta}, Nlink: 1}, nil
}

func (d *dirHandle) Mkdir(name string, meta tree.Meta) (Dir, error) {
	n, err := d.newChild(name, meta, fs.ModeDir)
	if err != nil {
		return nil, err
	}
	n.Nlink = 2 // "." plus the entry we are about to add in the parent
	d.n.Nlink++ // the child's ".." points back at this directory
	d.add(name, n)
	return &dirHandle{m: d.m, n: n}, nil
}

func (d *dirHandle) Create(name string, c tree.Source, meta tree.Meta) (File, error) {
	n, err := d.newChild(name, meta, 0) // regular file
	if err != nil {
		return nil, err
	}
	n.Content = c
	d.add(name, n)
	return &fileHandle{n: n}, nil
}

func (d *dirHandle) Symlink(name, target string, meta tree.Meta) error {
	n, err := d.newChild(name, meta, fs.ModeSymlink)
	if err != nil {
		return err
	}
	n.Link = target
	d.add(name, n)
	return nil
}

func (d *dirHandle) Mknod(name string, rdev uint64, meta tree.Meta) error {
	// The caller selects char vs block via meta.Mode's device bits.
	typeBits := meta.Mode & fs.ModeType
	if typeBits&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) == 0 {
		typeBits = fs.ModeDevice
	}
	n, err := d.newChild(name, meta, typeBits)
	if err != nil {
		return err
	}
	n.Rdev = rdev
	d.add(name, n)
	return nil
}

func (d *dirHandle) Link(name string, target File) error {
	if err := checkName(name); err != nil {
		return err
	}
	fh, ok := target.(*fileHandle)
	if !ok {
		return errBadLink
	}
	if d.find(name) != nil {
		return fs.ErrExist
	}
	fh.n.Nlink++
	d.add(name, fh.n)
	return nil
}

func (d *dirHandle) Lookup(name string) (*tree.Dirent, error) {
	e := d.find(name)
	if e == nil {
		return nil, fs.ErrNotExist
	}
	return &tree.Dirent{Name: e.Name, Inode: &e.Node.Inode}, nil
}

func (d *dirHandle) Remove(name string) error {
	for i := range d.n.Children {
		if d.n.Children[i].Name != name {
			continue
		}
		n := d.n.Children[i].Node
		if n.IsDir() {
			if len(n.Children) != 0 {
				return errNotEmpty
			}
			d.n.Nlink-- // the removed child's ".." is gone
		}
		n.Nlink--
		d.n.Children = append(d.n.Children[:i], d.n.Children[i+1:]...)
		return nil
	}
	return fs.ErrNotExist
}

func (d *dirHandle) Range(fn func(tree.Dirent) error) error {
	for i := range d.n.Children {
		e := &d.n.Children[i]
		if err := fn(tree.Dirent{Name: e.Name, Inode: &e.Node.Inode}); err != nil {
			return err
		}
	}
	return nil
}

// SubDir returns the child directory named name, for engines and callers that
// navigate the tree after building it.
func (d *dirHandle) SubDir(name string) (Dir, error) {
	e := d.find(name)
	if e == nil {
		return nil, fs.ErrNotExist
	}
	if !e.Node.IsDir() {
		return nil, errNotDir
	}
	return &dirHandle{m: d.m, n: e.Node}, nil
}
