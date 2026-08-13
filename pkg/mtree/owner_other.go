//go:build !unix

package mtree

import (
	"io/fs"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// hostOwner has nothing to read on platforms without Unix file metadata; the
// generated specification says uid 0 gid 0, which is a sensible thing to edit.
func hostOwner(fs.FileInfo, *image.Node) {}
