package exfat

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"
	"unicode/utf16"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Open reads an existing exFAT volume into the agnostic tree, so exFAT can be a
// conversion source. Directory metadata is parsed eagerly; file contents stay
// lazy, read per cluster on demand. The returned image is read-only: exFAT is
// write-once for fsforge, so Finalize reports that it cannot be re-finalized in
// place (rebuild via convert to change it).
func (e *ExFAT) Open(dev device.Device) (image.Image, error) {
	r, err := newReader(dev)
	if err != nil {
		return nil, err
	}
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	// The root directory is located by the boot sector and FAT-chained (no size).
	if err := r.readDir(root, r.rootFirst, 0, false); err != nil {
		return nil, err
	}
	return &exfatImageRead{Mem: image.Adopt(e.deps, root)}, nil
}

// exfatImageRead is an opened (read-only) exFAT image. Rebuild via convert to
// write changes; in-place re-finalize is not supported.
type exfatImageRead struct{ *image.Mem }

func (exfatImageRead) Finalize() error {
	return errors.New("exfat: cannot re-finalize an opened image; rebuild via convert")
}

// reader holds the parsed geometry and the FAT of an exFAT volume.
type reader struct {
	dev          device.Device
	bytesPerSec  uint64
	clusterBytes uint64
	heapStartSec uint64
	clusterCount uint32
	rootFirst    uint32
	fat          []byte
}

func newReader(dev device.Device) (*reader, error) {
	boot := make([]byte, bytesPerSector)
	if _, err := dev.ReadAt(boot, 0); err != nil && err != io.EOF {
		return nil, err
	}
	if string(boot[3:11]) != "EXFAT   " {
		return nil, errors.New("exfat: bad boot signature")
	}
	bpsShift := boot[108]
	spcShift := boot[109]
	if bpsShift < 9 || bpsShift > 12 || spcShift > 25 {
		return nil, fmt.Errorf("exfat: implausible sector/cluster shifts %d/%d", bpsShift, spcShift)
	}
	r := &reader{
		dev:          dev,
		bytesPerSec:  1 << bpsShift,
		clusterBytes: 1 << (uint(bpsShift) + uint(spcShift)),
		heapStartSec: uint64(le.Uint32(boot[88:])),
		clusterCount: le.Uint32(boot[92:]),
		rootFirst:    le.Uint32(boot[96:]),
	}

	fatOffsetSec := uint64(le.Uint32(boot[80:]))
	fatLengthSec := uint64(le.Uint32(boot[84:]))
	r.fat = make([]byte, fatLengthSec*r.bytesPerSec)
	if _, err := dev.ReadAt(r.fat, int64(fatOffsetSec*r.bytesPerSec)); err != nil && err != io.EOF {
		return nil, err
	}
	return r, nil
}

// clusterByteOffset returns the absolute byte offset of a data cluster (>= 2).
func (r *reader) clusterByteOffset(cluster uint32) int64 {
	return int64((r.heapStartSec + uint64(cluster-2)*(r.clusterBytes/r.bytesPerSec)) * r.bytesPerSec)
}

// fatNext returns the FAT successor of cluster c, or 0 if c is out of range.
func (r *reader) fatNext(c uint32) uint32 {
	if uint64(c)*4+4 > uint64(len(r.fat)) {
		return 0
	}
	return le.Uint32(r.fat[c*4:])
}

// clusterAt resolves the idx-th cluster of a chain starting at first, either
// contiguously (NoFatChain) or by walking the FAT.
func (r *reader) clusterAt(first, idx uint32, noFatChain bool) uint32 {
	if noFatChain {
		return first + idx
	}
	c := first
	for i := uint32(0); i < idx; i++ {
		c = r.fatNext(c)
		if c < 2 || c >= 0xFFFFFFF7 {
			return 0
		}
	}
	return c
}

// readDirBytes returns the raw bytes of a directory. With noFatChain the size is
// known and clusters are contiguous; otherwise the FAT chain is followed to its
// end (used for the root, whose size the boot sector does not record).
func (r *reader) readDirBytes(first uint32, size uint64, noFatChain bool) ([]byte, error) {
	var clusters []uint32
	if noFatChain {
		n := (size + r.clusterBytes - 1) / r.clusterBytes
		for i := uint32(0); uint64(i) < n; i++ {
			clusters = append(clusters, first+i)
		}
	} else {
		for c := first; c >= 2 && c < 0xFFFFFFF7; c = r.fatNext(c) {
			clusters = append(clusters, c)
			if uint32(len(clusters)) > r.clusterCount {
				return nil, errors.New("exfat: directory cluster chain too long (loop?)")
			}
		}
	}
	buf := make([]byte, len(clusters)*int(r.clusterBytes))
	for i, c := range clusters {
		if _, err := r.dev.ReadAt(buf[i*int(r.clusterBytes):(i+1)*int(r.clusterBytes)], r.clusterByteOffset(c)); err != nil && err != io.EOF {
			return nil, err
		}
	}
	return buf, nil
}

// readDir parses a directory's entry sets into dir's children, recursing into
// subdirectories.
func (r *reader) readDir(dir *image.Node, first uint32, size uint64, noFatChain bool) error {
	data, err := r.readDirBytes(first, size, noFatChain)
	if err != nil {
		return err
	}
	for off := 0; off+dirEntrySize <= len(data); {
		typ := data[off]
		switch {
		case typ == 0x00:
			return nil // end of directory
		case typ == entFile: // 0x85: a file/dir entry set
			n, err := r.parseFileSet(dir, data[off:])
			if err != nil {
				return err
			}
			off += n * dirEntrySize
		default:
			off += dirEntrySize // label, bitmap, up-case, or unused slot
		}
	}
	return nil
}

// parseFileSet reads one File + Stream + Name entry set at b and appends the
// resulting node to dir. It returns the number of 32-byte entries consumed.
func (r *reader) parseFileSet(dir *image.Node, b []byte) (int, error) {
	secondary := int(b[1])
	count := 1 + secondary
	if count*dirEntrySize > len(b) || secondary < 1 {
		return 1, nil // truncated/garbage set: skip the leading entry
	}
	attrs := le.Uint16(b[4:])
	mod := decodeTimestamp(le.Uint32(b[12:]))

	stream := b[dirEntrySize:]
	if stream[0] != entStream {
		return count, nil // not the set we expect; skip it whole
	}
	noFatChain := stream[1]&flagNoFatChain != 0
	nameLen := int(stream[3])
	firstClus := le.Uint32(stream[20:])
	dataLength := le.Uint64(stream[24:])

	// File Name entries carry 15 UTF-16 units each.
	units := make([]uint16, 0, nameLen)
	for k := 0; k < secondary-1 && len(units) < nameLen; k++ {
		ne := b[(2+k)*dirEntrySize:]
		if ne[0] != entFileName {
			break
		}
		for j := 0; j < 15 && len(units) < nameLen; j++ {
			units = append(units, le.Uint16(ne[2+j*2:]))
		}
	}
	name := string(utf16.Decode(units))

	meta := tree.Meta{Mode: 0o644, ModTime: mod}
	node := &image.Node{Inode: tree.Inode{Meta: meta}, Nlink: 1}
	if attrs&attrDirectory != 0 {
		node.Mode = fs.ModeDir | 0o755
		node.Nlink = 2
		if dataLength > 0 {
			if err := r.readDir(node, firstClus, dataLength, noFatChain); err != nil {
				return count, err
			}
		}
	} else if dataLength > 0 {
		node.Content = &exfatFile{r: r, first: firstClus, size: int64(dataLength), noFatChain: noFatChain}
	} else {
		node.Content = tree.Bytes(nil)
	}
	dir.Children = append(dir.Children, image.Entry{Name: name, Node: node})
	return count, nil
}

// decodeTimestamp inverts the exFAT 4-byte timestamp packing.
func decodeTimestamp(packed uint32) time.Time {
	return time.Date(
		1980+int(packed>>25&0x7f),
		time.Month(packed>>21&0x0f),
		int(packed>>16&0x1f),
		int(packed>>11&0x1f),
		int(packed>>5&0x3f),
		int(packed&0x1f)*2,
		0, time.UTC,
	)
}

// exfatFile is a lazy tree.Source over an exFAT file, reading cluster by cluster
// and resolving each cluster contiguously or through the FAT.
type exfatFile struct {
	r          *reader
	first      uint32
	size       int64
	noFatChain bool
}

func (f *exfatFile) Size() int64 { return f.size }

func (f *exfatFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("exfat: negative offset")
	}
	cb := int64(f.r.clusterBytes)
	n := 0
	for n < len(p) && off < f.size {
		clus := f.r.clusterAt(f.first, uint32(off/cb), f.noFatChain)
		if clus < 2 {
			break
		}
		within := off % cb
		toRead := cb - within
		if rem := int64(len(p) - n); rem < toRead {
			toRead = rem
		}
		if rem := f.size - off; rem < toRead {
			toRead = rem
		}
		if _, err := f.r.dev.ReadAt(p[n:n+int(toRead)], f.r.clusterByteOffset(clus)+within); err != nil && err != io.EOF {
			return n, err
		}
		n += int(toRead)
		off += toRead
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
