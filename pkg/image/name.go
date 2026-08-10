package image

import (
	"errors"
	"fmt"
	"strings"
)

// ErrBadName reports a directory entry name no filesystem tree may hold.
var ErrBadName = errors.New("image: invalid name")

// ValidName checks name against the one rule every filesystem shares: an entry
// is not empty, is neither "." nor "..", and holds no path separator.
//
// This is a security boundary, not only a correctness one. Readers build trees
// out of images they did not write, and every name in them is attacker-supplied
// input. A dirent named ".." collides with the real "..", corrupting whichever
// directory it lands in; worse, once such a tree reaches ExtractToDir the name
// is joined onto a host path and walks straight out of the extraction root. The
// editing API (Dir.Mkdir, Dir.Create, …) has always enforced this, so anything
// building a tree *around* that API — every engine's Open, the OCI flatten —
// must enforce it too. AddChild is the way to do that without thinking about it.
func ValidName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return fmt.Errorf("%w: %q", ErrBadName, name)
	}
	return nil
}

// AddChild appends child to n under name, rejecting names ValidName refuses.
//
// Readers must use this rather than appending to Children directly: it is the
// single place where a name parsed out of an untrusted image is checked before
// it enters the tree.
func (n *Node) AddChild(name string, child *Node) error {
	if err := ValidName(name); err != nil {
		return err
	}
	n.Children = append(n.Children, Entry{Name: name, Node: child})
	return nil
}
