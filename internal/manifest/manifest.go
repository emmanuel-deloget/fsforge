// Package manifest flattens a filesystem tree into a sorted list of comparable
// entries, so two trees can be diffed field by field.
//
// It exists because "the image is valid" and "the image holds what we put in
// it" are different claims. fsck answers the first; only a full readback
// answers the second. A manifest can be taken from an in-memory fsforge tree or
// from a directory an external tool extracted, which makes the two directly
// comparable and turns a round trip into one assertion instead of a dozen
// hand-written field checks.
//
// Comparison is field-selective on purpose. Formats lose different things —
// romfs has no mtime, FAT no owner, ISO without Rock Ridge no permissions — so
// a caller states which fields the format under test is expected to keep, and
// the rest are ignored rather than silently dropped from the model.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// Entry is one node, flattened to comparable scalars.
type Entry struct {
	Path   string      // slash-separated, relative to the root, no leading "/"
	Mode   fs.FileMode // type bits and permission bits
	UID    uint32
	GID    uint32
	MTime  time.Time
	Nlink  int
	Size   int64  // regular files only
	Digest string // sha256 of contents, regular files only
	Link   string // symlink target
	Rdev   uint64 // device nodes only

	// LinkGroup identifies hard-linked entries: every path sharing one inode
	// carries the same non-zero number, numbered by first appearance in path
	// order; unshared entries carry zero. Comparing the numbers proves the
	// *sharing* survived, which the link count alone does not — a format could
	// report nlink 2 on two unrelated inodes and still look correct.
	LinkGroup int

	Xattrs map[string][]byte
}

// Manifest is a set of entries ordered by path.
type Manifest []Entry

// FromTree flattens an fsforge build tree. Contents are hashed through the lazy
// tree.Source, so nothing is buffered beyond one copy window.
func FromTree(root *image.Node) (Manifest, error) {
	var rows []row
	if err := walkTree(root, "", &rows); err != nil {
		return nil, err
	}
	return assemble(rows), nil
}

// row pairs an entry with the identity that decides its link group: the *Node
// for a tree, the inode number for a host directory.
type row struct {
	e  Entry
	id any
}

func walkTree(n *image.Node, prefix string, rows *[]row) error {
	kids := append([]image.Entry(nil), n.Children...)
	sort.Slice(kids, func(i, j int) bool { return kids[i].Name < kids[j].Name })
	for _, e := range kids {
		p := path.Join(prefix, e.Name)
		ent, err := entryOf(p, e.Node)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		*rows = append(*rows, row{e: ent, id: e.Node})
		if e.Node.IsDir() {
			if err := walkTree(e.Node, p, rows); err != nil {
				return err
			}
		}
	}
	return nil
}

// assemble sorts by path, then numbers the hard-link groups in that order so
// the numbering is a property of the tree's shape and not of traversal order.
func assemble(rows []row) Manifest {
	sort.Slice(rows, func(i, j int) bool { return rows[i].e.Path < rows[j].e.Path })

	seen := make(map[any]int, len(rows))
	for _, r := range rows {
		seen[r.id]++
	}
	group := map[any]int{}
	out := make(Manifest, len(rows))
	for i, r := range rows {
		if seen[r.id] > 1 {
			g, ok := group[r.id]
			if !ok {
				g = len(group) + 1
				group[r.id] = g
			}
			r.e.LinkGroup = g
		}
		out[i] = r.e
	}
	return out
}

func entryOf(p string, n *image.Node) (Entry, error) {
	e := Entry{
		Path:   p,
		Mode:   n.Mode,
		UID:    n.UID,
		GID:    n.GID,
		MTime:  n.ModTime,
		Nlink:  n.Nlink,
		Link:   n.Link,
		Rdev:   n.Rdev,
		Xattrs: n.Xattrs,
	}
	if n.Mode.IsRegular() && n.Content != nil {
		e.Size = n.Content.Size()
		d, err := digestOf(n.Content, e.Size)
		if err != nil {
			return e, err
		}
		e.Digest = d
	}
	return e, nil
}

func digestOf(src io.ReaderAt, size int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(src, 0, size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
