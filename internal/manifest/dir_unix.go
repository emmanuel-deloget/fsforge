//go:build unix

package manifest

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// FromDir flattens a host directory — typically one an external tool extracted
// an image into — so it can be diffed against the tree fsforge wrote.
//
// Hard links are recovered from the inode number rather than the link count, so
// the grouping means the same thing on both sides. Symlinks are not followed.
func FromDir(root string) (Manifest, error) {
	var rows []row
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		fi, err := d.Info() // Lstat semantics: WalkDir does not follow links
		if err != nil {
			return err
		}
		e := Entry{
			Path:  rel,
			Mode:  fi.Mode(),
			MTime: fi.ModTime(),
			Nlink: 1,
		}
		var id any = rel // unshared by default: each path is its own identity
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			e.UID, e.GID = st.Uid, st.Gid
			e.Nlink = int(st.Nlink)
			if isDevice(fi.Mode()) {
				e.Rdev = uint64(st.Rdev)
			}
			if st.Nlink > 1 && fi.Mode().IsRegular() {
				id = [2]uint64{uint64(st.Dev), uint64(st.Ino)}
			}
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			e.Link = target
		case fi.Mode().IsRegular():
			e.Size = fi.Size()
			d, err := fileDigest(p, fi.Size())
			if err != nil {
				return err
			}
			e.Digest = d
		}
		x, err := readXattrs(p, fi.Mode())
		if err != nil {
			return err
		}
		e.Xattrs = x

		rows = append(rows, row{e: e, id: id})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return assemble(rows), nil
}

func fileDigest(p string, size int64) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return digestOf(f, size)
}
