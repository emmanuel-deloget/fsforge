//go:build unix

package mtree

import (
	"io/fs"
	"syscall"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// hostOwner records the owner a host file actually has, which is normally the
// person running the build rather than the one the image needs — the gap this
// package exists to close.
func hostOwner(fi fs.FileInfo, n *image.Node) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		n.UID, n.GID = st.Uid, st.Gid
		if fi.Mode()&(fs.ModeDevice|fs.ModeCharDevice) != 0 {
			n.Rdev = uint64(st.Rdev)
		}
	}
}
