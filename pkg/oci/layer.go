package oci

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"io/fs"
	"path"
	"sort"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// writeLayer serialises the whole tree rooted at root into a tar layer blob in
// l, optionally gzip-compressed. It returns the stored (compressed) blob's
// descriptor and the diffID (sha256 of the uncompressed tar), both needed by
// the manifest and config respectively. Contents are streamed, never buffered.
func writeLayer(l *Layout, root *image.Node, useGzip bool) (Descriptor, string, error) {
	return streamLayer(l, useGzip, func(tw *tar.Writer) error { return tarTree(tw, root) })
}

// streamLayer writes a tar layer whose entries are produced by fill, hashing the
// uncompressed tar to derive the diffID while optionally gzip-compressing the
// stored blob. It is the shared core of writeLayer (a full tree) and
// writeDiffLayer (a delta against a lower state).
func streamLayer(l *Layout, useGzip bool, fill func(*tar.Writer) error) (Descriptor, string, error) {
	var diff hash.Hash
	desc, err := l.PutBlobStream(layerMediaType(useGzip), func(w io.Writer) error {
		diff = sha256.New()
		var tarDst io.Writer = w
		var gz *gzip.Writer
		if useGzip {
			gz = gzip.NewWriter(w)
			tarDst = gz
		}
		tw := tar.NewWriter(io.MultiWriter(tarDst, diff))
		if err := fill(tw); err != nil {
			return err
		}
		if err := tw.Close(); err != nil {
			return err
		}
		if gz != nil {
			return gz.Close()
		}
		return nil
	})
	if err != nil {
		return Descriptor{}, "", err
	}
	return desc, "sha256:" + hex.EncodeToString(diff.Sum(nil)), nil
}

// tarTree walks the tree depth-first in sorted order, emitting tar entries with
// relative paths. Shared regular-file nodes (hard links) are emitted once as
// data and again as tar hard links.
func tarTree(tw *tar.Writer, root *image.Node) error {
	seen := map[*image.Node]string{}
	var walk func(n *image.Node, prefix string) error
	walk = func(n *image.Node, prefix string) error {
		for _, e := range sortedChildren(n) {
			name := path.Join(prefix, e.Name)
			if err := writeEntry(tw, e.Node, name, seen); err != nil {
				return err
			}
			if e.Node.IsDir() {
				if err := walk(e.Node, name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(root, "")
}

func writeEntry(tw *tar.Writer, n *image.Node, name string, seen map[*image.Node]string) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    int64(n.Mode.Perm()),
		Uid:     int(n.UID),
		Gid:     int(n.GID),
		ModTime: n.ModTime,
	}
	if len(n.Xattrs) > 0 {
		hdr.PAXRecords = map[string]string{}
		for k, v := range n.Xattrs {
			hdr.PAXRecords["SCHILY.xattr."+k] = string(v)
		}
	}
	switch {
	case n.IsDir():
		hdr.Typeflag = tar.TypeDir
		hdr.Name = name + "/"
		hdr.Mode = int64(n.Mode.Perm())
		return tw.WriteHeader(hdr)
	case n.Mode&fs.ModeSymlink != 0:
		hdr.Typeflag = tar.TypeSymlink
		hdr.Linkname = n.Link
		return tw.WriteHeader(hdr)
	case n.Mode&fs.ModeCharDevice != 0:
		hdr.Typeflag = tar.TypeChar
		hdr.Devmajor, hdr.Devminor = devNums(n.Rdev)
		return tw.WriteHeader(hdr)
	case n.Mode&fs.ModeDevice != 0:
		hdr.Typeflag = tar.TypeBlock
		hdr.Devmajor, hdr.Devminor = devNums(n.Rdev)
		return tw.WriteHeader(hdr)
	case n.Mode&fs.ModeNamedPipe != 0:
		hdr.Typeflag = tar.TypeFifo
		return tw.WriteHeader(hdr)
	default: // regular file
		if first, ok := seen[n]; ok {
			hdr.Typeflag = tar.TypeLink
			hdr.Linkname = first
			return tw.WriteHeader(hdr)
		}
		if n.Nlink > 1 {
			seen[n] = name
		}
		var size int64
		if n.Content != nil {
			size = n.Content.Size()
		}
		hdr.Typeflag = tar.TypeReg
		hdr.Size = size
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if n.Content != nil && size > 0 {
			_, err := io.Copy(tw, io.NewSectionReader(n.Content, 0, size))
			return err
		}
		return nil
	}
}

func devNums(rdev uint64) (int64, int64) {
	return int64(rdev >> 8), int64(rdev & 0xff)
}

func sortedChildren(n *image.Node) []image.Entry {
	out := append([]image.Entry(nil), n.Children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
