package iso

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Open reads an existing ISO9660 image into the agnostic tree, so ISO can be a
// conversion source. POSIX names, permissions, symlinks and device nodes are
// recovered from the Rock Ridge (SUSP) entries fsforge writes; a plain ISO9660
// volume without Rock Ridge yields the upper-cased short identifiers. The
// returned image is read-only (ISO9660 is read-only on disk; rebuild to change
// it).
func (e *ISO) Open(dev device.Device) (image.Image, error) {
	r := &reader{dev: dev}
	pvd := make([]byte, sectorSize)
	if _, err := dev.ReadAt(pvd, pvdSector*sectorSize); err != nil && err != io.EOF {
		return nil, err
	}
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" {
		return nil, errors.New("iso: no primary volume descriptor (bad CD001 signature)")
	}
	// The root directory record sits at offset 156 of the PVD.
	rootExtent := le.Uint32(pvd[156+2:])
	rootLen := le.Uint32(pvd[156+10:])

	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	if err := r.readDir(root, rootExtent, rootLen); err != nil {
		return nil, err
	}
	return &isoImageRead{Mem: image.Adopt(e.deps, root)}, nil
}

// isoImageRead is an opened (read-only) ISO image. Rebuild via convert to write
// changes; in-place re-finalize is not supported.
type isoImageRead struct{ *image.Mem }

func (isoImageRead) Finalize() error {
	return errors.New("iso: cannot re-finalize an opened image; rebuild via convert")
}

type reader struct{ dev device.Device }

// readDir parses the directory at extent (in 2048-byte sectors) of dataLen bytes
// into dir's children, recursing into subdirectories. The "." and ".." records
// are skipped.
func (r *reader) readDir(dir *image.Node, extent, dataLen uint32) error {
	data := make([]byte, sectors(uint64(dataLen))*sectorSize)
	if _, err := r.dev.ReadAt(data, int64(extent)*sectorSize); err != nil && err != io.EOF {
		return err
	}

	for off := 0; off < int(dataLen); {
		recLen := int(data[off])
		if recLen == 0 {
			// Records never straddle a sector; pad to the next one.
			next := (off/sectorSize + 1) * sectorSize
			if next <= off {
				break
			}
			off = next
			continue
		}
		if off+recLen > len(data) {
			break
		}
		rec := data[off : off+recLen]
		off += recLen

		idLen := int(rec[32])
		if 33+idLen > len(rec) {
			continue
		}
		id := rec[33 : 33+idLen]
		if idLen == 1 && (id[0] == 0x00 || id[0] == 0x01) {
			continue // "." or ".."
		}

		childExtent := le.Uint32(rec[2:])
		childLen := le.Uint32(rec[10:])
		isDir := rec[25]&0x02 != 0

		base := 33 + idLen
		if base%2 == 1 {
			base++
		}
		rr := parseSUSP(rec[base:])

		name := rr.name
		if name == "" {
			name = stripVersion(string(id))
		}
		node := &image.Node{Nlink: 1}
		node.Meta = tree.Meta{Mode: rr.mode(isDir), ModTime: rr.modTime}

		switch {
		case node.Mode.IsDir():
			node.Nlink = 2
			if err := r.readDir(node, childExtent, childLen); err != nil {
				return err
			}
		case node.Mode&fs.ModeSymlink != 0:
			node.Link = rr.symlink
		case node.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
			node.Rdev = rr.rdev
		default:
			if childLen > 0 {
				node.Content = &isoFile{dev: r.dev, off: int64(childExtent) * sectorSize, size: int64(childLen)}
			} else {
				node.Content = tree.Bytes(nil)
			}
		}
		dir.Children = append(dir.Children, image.Entry{Name: name, Node: node})
	}
	return nil
}

// rrInfo holds the Rock Ridge attributes recovered from a record's system-use
// area. posixMode is zero when no PX entry was present.
type rrInfo struct {
	name      string
	symlink   string
	posixMode uint32
	rdev      uint64
	modTime   time.Time
}

// mode resolves the file mode, falling back to the ISO directory flag when no PX
// entry supplied a POSIX mode.
func (rr rrInfo) mode(isDir bool) fs.FileMode {
	if rr.posixMode != 0 {
		return decodePOSIXMode(rr.posixMode)
	}
	if isDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}

// parseSUSP walks the SUSP/Rock Ridge entries in a directory record's system-use
// area, collecting the name (NM), POSIX attributes (PX), symlink target (SL),
// device numbers (PN) and modify time (TF). Unknown entries (SP, ER, CE, …) are
// skipped.
func parseSUSP(sua []byte) rrInfo {
	var rr rrInfo
	var name strings.Builder
	for off := 0; off+4 <= len(sua); {
		sig := string(sua[off : off+2])
		entLen := int(sua[off+2])
		if entLen < 4 || off+entLen > len(sua) {
			break
		}
		body := sua[off+4 : off+entLen]
		switch sig {
		case "NM":
			if len(body) >= 1 {
				name.Write(body[1:]) // body[0] is the NM flags byte
			}
		case "PX":
			if len(body) >= 4 {
				rr.posixMode = le.Uint32(body[0:])
			}
		case "SL":
			rr.symlink += decodeSL(body)
		case "PN":
			if len(body) >= 16 {
				rr.rdev = uint64(le.Uint32(body[0:]))<<32 | uint64(le.Uint32(body[8:]))
			}
		case "TF":
			rr.modTime = decodeTF(body)
		}
		off += entLen
	}
	rr.name = name.String()
	return rr
}

// decodeSL reconstructs a symlink target from one SL entry's component records.
func decodeSL(body []byte) string {
	if len(body) < 1 {
		return ""
	}
	var parts []string
	for i := 1; i+2 <= len(body); {
		flags := body[i]
		clen := int(body[i+1])
		if i+2+clen > len(body) {
			break
		}
		content := string(body[i+2 : i+2+clen])
		switch {
		case flags&0x08 != 0: // ROOT
			content = ""
		case flags&0x02 != 0: // CURRENT
			content = "."
		case flags&0x04 != 0: // PARENT
			content = ".."
		}
		parts = append(parts, content)
		i += 2 + clen
	}
	return strings.Join(parts, "/")
}

// decodeTF reads the modify time from a TF entry (only the MODIFY bit is set by
// fsforge's writer).
func decodeTF(body []byte) time.Time {
	if len(body) < 1 {
		return time.Time{}
	}
	flags := body[0]
	off := 1
	// Times appear in flag-bit order: CREATION(1), MODIFY(2), ACCESS(4), …
	if flags&0x01 != 0 { // skip CREATION if present
		off += 7
	}
	if flags&0x02 != 0 && off+7 <= len(body) {
		return decodeDirTime(body[off : off+7])
	}
	return time.Time{}
}

// decodeDirTime parses the 7-byte ISO9660 directory date/time.
func decodeDirTime(b []byte) time.Time {
	return time.Date(1900+int(b[0]), time.Month(b[1]), int(b[2]),
		int(b[3]), int(b[4]), int(b[5]), 0, time.UTC)
}

// decodePOSIXMode turns a Rock Ridge st_mode into a Go file mode.
func decodePOSIXMode(m uint32) fs.FileMode {
	mode := fs.FileMode(m & 0o777)
	if m&0o4000 != 0 {
		mode |= fs.ModeSetuid
	}
	if m&0o2000 != 0 {
		mode |= fs.ModeSetgid
	}
	if m&0o1000 != 0 {
		mode |= fs.ModeSticky
	}
	switch m & 0o170000 {
	case 0o040000:
		mode |= fs.ModeDir
	case 0o120000:
		mode |= fs.ModeSymlink
	case 0o020000:
		mode |= fs.ModeCharDevice | fs.ModeDevice
	case 0o060000:
		mode |= fs.ModeDevice
	case 0o010000:
		mode |= fs.ModeNamedPipe
	case 0o140000:
		mode |= fs.ModeSocket
	}
	return mode
}

// stripVersion removes the ISO9660 ";1" version suffix from an identifier.
func stripVersion(id string) string {
	if i := strings.IndexByte(id, ';'); i >= 0 {
		return id[:i]
	}
	return id
}

// isoFile is a lazy tree.Source over a single-extent ISO file.
type isoFile struct {
	dev  device.Device
	off  int64
	size int64
}

func (f *isoFile) Size() int64 { return f.size }

func (f *isoFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("iso: negative offset")
	}
	if off >= f.size {
		return 0, io.EOF
	}
	if rem := f.size - off; int64(len(p)) > rem {
		n, err := f.dev.ReadAt(p[:rem], f.off+off)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return f.dev.ReadAt(p, f.off+off)
}
