// Package mtree reads and writes mtree(5) specifications: the text format BSD,
// Yocto and Buildroot use to describe a filesystem tree's ownership, modes and
// special files.
//
// It exists to close the gap between what a build can produce and what an image
// needs. A checkout is owned by whoever cloned it, holds no device nodes and
// cannot carry a setuid bit through most CI; a rootfs needs uid 0,
// /dev/console and a setuid ping. Without a way to say so out of band,
// "build a rootfs without root" stops halfway. This is that way, and choosing
// mtree over an invented format means the same file works with mkfs from
// e2fsprogs, with libarchive, and with the tooling those two ecosystems already
// have.
//
// A spec is applied over a tree: entries that exist are amended, entries that do
// not are created. That ordering matters — the common shape is to populate from
// a directory and then lay a spec over the result.
package mtree

import (
	"fmt"
	"io/fs"
	"time"
)

// Entry is one line of a specification: a path and the keywords set for it.
// Fields that were not mentioned are nil, which is what lets Apply amend an
// existing node without overwriting everything else about it.
type Entry struct {
	// Path is slash-separated and relative to the tree root, with no leading
	// "./" or "/".
	Path string

	Type     *NodeType
	UID      *uint32
	GID      *uint32
	Mode     *fs.FileMode // permission bits, including setuid/setgid/sticky
	Link     *string      // symlink target
	Major    *uint32      // device major, when Type is Block or Char
	Minor    *uint32      // device minor
	Size     *int64
	Time     *time.Time
	Contents *string           // host file to take this entry's contents from
	Xattrs   map[string][]byte // from xattr.<name>= keywords

	// Line is where the entry came from, for error messages.
	Line int
}

// NodeType is an mtree type keyword.
type NodeType uint8

const (
	TypeFile NodeType = iota
	TypeDir
	TypeLink
	TypeBlock
	TypeChar
	TypeFifo
	TypeSocket
)

var typeNames = map[string]NodeType{
	"file": TypeFile, "dir": TypeDir, "link": TypeLink,
	"block": TypeBlock, "char": TypeChar, "fifo": TypeFifo, "socket": TypeSocket,
}

func (t NodeType) String() string {
	for name, v := range typeNames {
		if v == t {
			return name
		}
	}
	return "file"
}

// mode maps a type onto the Go file-mode bits that select it.
func (t NodeType) mode() fs.FileMode {
	switch t {
	case TypeDir:
		return fs.ModeDir
	case TypeLink:
		return fs.ModeSymlink
	case TypeBlock:
		return fs.ModeDevice
	case TypeChar:
		return fs.ModeDevice | fs.ModeCharDevice
	case TypeFifo:
		return fs.ModeNamedPipe
	case TypeSocket:
		return fs.ModeSocket
	default:
		return 0
	}
}

// typeOf is the inverse, for writing a spec out.
func typeOf(m fs.FileMode) NodeType {
	switch {
	case m.IsDir():
		return TypeDir
	case m&fs.ModeSymlink != 0:
		return TypeLink
	case m&fs.ModeCharDevice != 0:
		return TypeChar
	case m&fs.ModeDevice != 0:
		return TypeBlock
	case m&fs.ModeNamedPipe != 0:
		return TypeFifo
	case m&fs.ModeSocket != 0:
		return TypeSocket
	default:
		return TypeFile
	}
}

// Spec is a parsed specification, in file order.
type Spec struct {
	Entries []Entry
}

// Error reports a problem at a specific line of a specification.
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("mtree: line %d: %s", e.Line, e.Msg) }

func errAt(line int, format string, args ...any) error {
	return &Error{Line: line, Msg: fmt.Sprintf(format, args...)}
}
