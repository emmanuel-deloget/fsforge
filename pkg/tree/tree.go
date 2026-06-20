// Package tree is the filesystem-agnostic logical model that all engines share.
// A directory hierarchy is described once, as a tree of inodes, and each engine
// is responsible only for laying that tree out on disk and parsing it back.
//
// File contents are never held in memory: an inode references its data through
// the Source interface, which is streamed at finalize time. Hard links fall out
// naturally because several Dirents may point at the same *Inode.
package tree

import (
	"errors"
	"io"
	"io/fs"
	"time"
)

// Source is a lazy, randomly addressable view of a regular file's contents.
// Implementations may be backed by memory, a host file, or another image.
type Source interface {
	io.ReaderAt
	Size() int64
}

// Meta is the metadata common to every node, independent of any filesystem.
type Meta struct {
	Mode    fs.FileMode
	UID     uint32
	GID     uint32
	ModTime time.Time // zero means "resolve from the injected Clock at build time"
	Xattrs  map[string][]byte
}

// Inode is a single filesystem object. Exactly one of Content/Link/Rdev is
// meaningful, selected by Mode; directories carry their entries out-of-band in
// the engine's working state rather than on the inode itself.
type Inode struct {
	Meta
	Content Source // regular file
	Link    string // symlink target
	Rdev    uint64 // device number for block/char devices
}

// Dirent binds a name within a directory to an inode. Sharing one *Inode across
// several Dirents expresses a hard link.
type Dirent struct {
	Name  string
	Inode *Inode
}

// Bytes is a Source backed by an in-memory slice, used for small files and
// tests.
type Bytes []byte

func (b Bytes) Size() int64 { return int64(len(b)) }

func (b Bytes) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("tree: negative offset")
	}
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
