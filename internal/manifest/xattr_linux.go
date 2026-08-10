//go:build linux

package manifest

import (
	"io/fs"
	"os"
	"syscall"
)

// readXattrs reads every extended attribute on p.
//
// Symlinks are skipped rather than read. The standard library only exposes the
// link-following Listxattr/Getxattr — the Lxxx variants live in x/sys, which
// the zero-dependency rule rules out — so reading a symlink here would report
// its *target's* attributes as its own, which is worse than reporting none.
// Nothing is lost in practice: Linux only permits security.* and trusted.* on a
// symlink, never the user.* and security.capability pairs a rootfs cares about.
//
// Only Linux is covered, which is where the conformance tools run.
func readXattrs(p string, mode fs.FileMode) (map[string][]byte, error) {
	if mode&fs.ModeSymlink != 0 {
		return nil, nil
	}
	size, err := syscall.Listxattr(p, nil)
	if err != nil || size == 0 {
		return nil, ignoreUnsupported(err)
	}
	buf := make([]byte, size)
	size, err = syscall.Listxattr(p, buf)
	if err != nil {
		return nil, ignoreUnsupported(err)
	}

	out := map[string][]byte{}
	for _, name := range splitNUL(buf[:size]) {
		vsize, err := syscall.Getxattr(p, name, nil)
		if err != nil {
			return nil, err
		}
		v := make([]byte, vsize)
		if vsize > 0 {
			if _, err := syscall.Getxattr(p, name, v); err != nil {
				return nil, err
			}
		}
		out[name] = v
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ignoreUnsupported turns "this filesystem has no xattrs" into no xattrs rather
// than an error: tmpfs and overlayfs differ here, and a comparison should not
// fail on where the extraction happened to land.
func ignoreUnsupported(err error) error {
	switch err {
	case nil, syscall.ENOTSUP, syscall.ENODATA, os.ErrNotExist:
		return nil
	}
	return err
}

func splitNUL(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == 0 {
			if i > start {
				out = append(out, string(b[start:i]))
			}
			start = i + 1
		}
	}
	return out
}
