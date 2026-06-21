package udf

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

// Open reads an existing UDF image into the agnostic tree, so UDF can be a
// conversion source. It follows the anchor at block 256 to the Volume
// Descriptor Sequence, locates the partition and File Set Descriptor, and walks
// the directory hierarchy. Both File Entries and Extended File Entries and the
// short/long/in-ICB allocation forms are understood. The returned image is
// read-only; rebuild via Convert to change it.
func (e *UDF) Open(dev device.Device) (image.Image, error) {
	r := &ureader{dev: dev}
	if err := r.readVolume(); err != nil {
		return nil, err
	}
	root, err := r.readFE(r.rootLbn, nil, map[uint32]*image.Node{})
	if err != nil {
		return nil, err
	}
	return &uImageRead{Mem: image.Adopt(e.deps, root)}, nil
}

type uImageRead struct{ *image.Mem }

func (uImageRead) Finalize() error {
	return errors.New("udf: cannot re-finalize an opened image; rebuild via convert")
}

type ureader struct {
	dev       device.Device
	partStart uint32
	rootLbn   uint32
}

func (r *ureader) block(abs uint32) ([]byte, error) {
	b := make([]byte, blockSize)
	if _, err := r.dev.ReadAt(b, int64(abs)*blockSize); err != nil && err != io.EOF {
		return nil, err
	}
	return b, nil
}

func (r *ureader) partBlock(lbn uint32) ([]byte, error) { return r.block(r.partStart + lbn) }

// readVolume follows the anchor to the volume descriptors, recording the
// partition start and the root directory's File Entry block.
func (r *ureader) readVolume() error {
	avdp, err := r.block(avdpBlock)
	if err != nil {
		return err
	}
	if le.Uint16(avdp[0:]) != tagAVDP {
		return errors.New("udf: no anchor volume descriptor at block 256")
	}
	mainLen := le.Uint32(avdp[16:])
	mainLoc := le.Uint32(avdp[20:])

	var fsdLbn uint32
	found := false
	for i := uint32(0); i < mainLen/blockSize; i++ {
		d, err := r.block(mainLoc + i)
		if err != nil {
			return err
		}
		switch le.Uint16(d[0:]) {
		case tagPD:
			r.partStart = le.Uint32(d[188:])
		case tagLVD:
			fsdLbn = le.Uint32(d[252:]) // logicalVolContentsUse extLocation
			found = true
		case tagTD:
			i = mainLen // stop at the terminating descriptor
		}
	}
	if !found {
		return errors.New("udf: no logical volume descriptor")
	}

	fsd, err := r.partBlock(fsdLbn)
	if err != nil {
		return err
	}
	if le.Uint16(fsd[0:]) != tagFSD {
		return errors.New("udf: file set descriptor not found")
	}
	r.rootLbn = le.Uint32(fsd[404:]) // rootDirectoryICB extLocation
	return nil
}

// feInfo is a parsed File Entry / Extended File Entry.
type feInfo struct {
	fileType uint8
	flags    uint16
	perm     uint32
	uid, gid uint32
	nlink    uint16
	infoLen  uint64
	mtime    time.Time
	ea, ad   []byte
}

func parseFE(b []byte) (feInfo, bool) {
	ident := le.Uint16(b[0:])
	if ident != tagFE && ident != tagEFE {
		return feInfo{}, false
	}
	var in feInfo
	in.fileType = b[16+11]
	in.flags = le.Uint16(b[16+18:])
	in.uid = le.Uint32(b[36:])
	in.gid = le.Uint32(b[40:])
	in.perm = le.Uint32(b[44:])
	in.nlink = le.Uint16(b[48:])
	in.infoLen = le.Uint64(b[56:])

	var mtimeOff, lenEAOff, header int
	if ident == tagEFE {
		mtimeOff, lenEAOff, header = 92, 208, 216
	} else {
		mtimeOff, lenEAOff, header = 84, 168, 176
	}
	in.mtime = readTimestamp(b[mtimeOff:])
	lenEA := int(le.Uint32(b[lenEAOff:]))
	lenAD := int(le.Uint32(b[lenEAOff+4:]))
	if header+lenEA+lenAD > len(b) {
		return feInfo{}, false
	}
	in.ea = b[header : header+lenEA]
	in.ad = b[header+lenEA : header+lenEA+lenAD]
	return in, true
}

// extents returns a file's data extents as (partition lbn, byte length) pairs.
// In-ICB data is handled by the caller, which reads in.ad directly.
func (in feInfo) extents() []extent {
	kind := in.flags & 0x7
	var out []extent
	switch kind {
	case adShort:
		for off := 0; off+8 <= len(in.ad); off += 8 {
			length := le.Uint32(in.ad[off:]) &^ extLenTypeMask
			pos := le.Uint32(in.ad[off+4:])
			if length == 0 {
				continue
			}
			out = append(out, extent{lbn: pos, length: length})
		}
	case 0x1: // long
		for off := 0; off+16 <= len(in.ad); off += 16 {
			length := le.Uint32(in.ad[off:]) &^ extLenTypeMask
			pos := le.Uint32(in.ad[off+4:])
			if length == 0 {
				continue
			}
			out = append(out, extent{lbn: pos, length: length})
		}
	}
	return out
}

// data materialises a node's full data (used for directories and symlinks).
func (r *ureader) data(in feInfo) ([]byte, error) {
	if in.flags&0x7 == adInICB {
		return append([]byte(nil), in.ad...), nil
	}
	var out []byte
	for _, e := range in.extents() {
		buf := make([]byte, (int64(e.length)+blockSize-1)/blockSize*blockSize)
		if _, err := r.dev.ReadAt(buf, int64(r.partStart+e.lbn)*blockSize); err != nil && err != io.EOF {
			return nil, err
		}
		out = append(out, buf[:e.length]...)
	}
	return out, nil
}

func (r *ureader) readFE(lbn uint32, parent *image.Node, seen map[uint32]*image.Node) (*image.Node, error) {
	if n, ok := seen[lbn]; ok {
		return n, nil
	}
	b, err := r.partBlock(lbn)
	if err != nil {
		return nil, err
	}
	in, ok := parseFE(b)
	if !ok {
		return nil, errors.New("udf: bad file entry")
	}

	n := &image.Node{Nlink: int(in.nlink)}
	n.Meta = tree.Meta{Mode: modeFromFE(in), UID: in.uid, GID: in.gid, ModTime: in.mtime}
	seen[lbn] = n

	switch in.fileType {
	case ftDirectory:
		dirData, err := r.data(in)
		if err != nil {
			return nil, err
		}
		if err := r.readDir(n, dirData, seen); err != nil {
			return nil, err
		}
	case ftSymlink:
		d, err := r.data(in)
		if err != nil {
			return nil, err
		}
		n.Link = decodeSymlink(d)
	case ftChar, ftBlock:
		n.Rdev = deviceFromEA(in.ea)
	case ftFIFO, ftSocket:
		// no payload
	default:
		if in.flags&0x7 == adInICB {
			n.Content = tree.Bytes(append([]byte(nil), in.ad...))
		} else {
			n.Content = &udfFile{dev: r.dev, partStart: r.partStart, extents: in.extents(), size: int64(in.infoLen)}
		}
	}
	_ = parent
	return n, nil
}

func (r *ureader) readDir(dir *image.Node, data []byte, seen map[uint32]*image.Node) error {
	for off := 0; off+38 <= len(data); {
		chars := data[off+18]
		lenFI := int(data[off+19])
		icbLbn := le.Uint32(data[off+24:]) // long_ad extLocation
		lenImpUse := int(le.Uint16(data[off+36:]))
		nameOff := off + 38 + lenImpUse
		fidLen := 38 + lenImpUse + lenFI
		if pad := fidLen % 4; pad != 0 {
			fidLen += 4 - pad
		}
		if nameOff+lenFI > len(data) {
			break
		}
		if chars&fidParent == 0 && chars&0x04 == 0 && lenFI > 0 { // skip parent and deleted
			name := decodeCS0(data[nameOff : nameOff+lenFI])
			child, err := r.readFE(icbLbn, dir, seen)
			if err != nil {
				return err
			}
			dir.Children = append(dir.Children, image.Entry{Name: name, Node: child})
		}
		off += fidLen
	}
	return nil
}

type extent struct {
	lbn    uint32
	length uint32
}

// udfFile is a lazy tree.Source over a regular file's data extents.
type udfFile struct {
	dev       device.Device
	partStart uint32
	extents   []extent
	size      int64
}

func (f *udfFile) Size() int64 { return f.size }

func (f *udfFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("udf: negative offset")
	}
	if off >= f.size {
		return 0, io.EOF
	}
	want := int64(len(p))
	if rem := f.size - off; want > rem {
		want = rem
	}
	var n int64
	var base int64
	for _, e := range f.extents {
		elen := int64(e.length)
		if off < base+elen {
			within := off - base
			chunk := elen - within
			if chunk > want-n {
				chunk = want - n
			}
			src := int64(f.partStart+e.lbn)*blockSize + within
			m, err := f.dev.ReadAt(p[n:n+chunk], src)
			n += int64(m)
			if err != nil && err != io.EOF {
				return int(n), err
			}
			off += int64(m)
		}
		base += elen
		if n >= want {
			break
		}
	}
	if n < int64(len(p)) {
		return int(n), io.EOF
	}
	return int(n), nil
}

// --- decoding helpers ---

func readTimestamp(b []byte) time.Time {
	year := int(le.Uint16(b[2:]))
	if year == 0 {
		return time.Time{}
	}
	return time.Date(year, time.Month(b[4]), int(b[5]), int(b[6]), int(b[7]), int(b[8]),
		int(b[9])*10_000_000, time.UTC)
}

func decodeCS0(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	switch b[0] {
	case 16:
		var sb strings.Builder
		for i := 1; i+1 < len(b); i += 2 {
			sb.WriteRune(rune(uint16(b[i])<<8 | uint16(b[i+1])))
		}
		return sb.String()
	default: // 8 (Latin-1)
		var sb strings.Builder
		for _, c := range b[1:] {
			sb.WriteRune(rune(c))
		}
		return sb.String()
	}
}

func decodeSymlink(b []byte) string {
	var parts []string
	abs := false
	for i := 0; i+4 <= len(b); {
		typ := b[i]
		clen := int(b[i+1])
		name := ""
		if clen > 0 && i+4+clen <= len(b) {
			name = decodeCS0(b[i+4 : i+4+clen])
		}
		switch typ {
		case 1, 2:
			abs = true
		case 3:
			parts = append(parts, "..")
		case 4:
			parts = append(parts, ".")
		case 5:
			parts = append(parts, name)
		}
		i += 4 + clen
	}
	s := strings.Join(parts, "/")
	if abs {
		s = "/" + s
	}
	return s
}

func deviceFromEA(ea []byte) uint64 {
	for o := 24; o+24 <= len(ea); {
		attrType := le.Uint32(ea[o:])
		attrLen := int(le.Uint32(ea[o+8:]))
		if attrType == 12 {
			major := le.Uint32(ea[o+16:])
			minor := le.Uint32(ea[o+20:])
			return uint64(major)<<8 | uint64(minor)
		}
		if attrLen <= 0 {
			break
		}
		o += attrLen
	}
	return 0
}

func modeFromFE(in feInfo) fs.FileMode {
	var m fs.FileMode
	p := in.perm
	set := func(bit uint32, mode fs.FileMode) {
		if p&bit != 0 {
			m |= mode
		}
	}
	set(permURead, 0o400)
	set(permUWrite, 0o200)
	set(permUExec, 0o100)
	set(permGRead, 0o040)
	set(permGWrite, 0o020)
	set(permGExec, 0o010)
	set(permORead, 0o004)
	set(permOWrite, 0o002)
	set(permOExec, 0o001)
	if in.flags&icbSetuid != 0 {
		m |= fs.ModeSetuid
	}
	if in.flags&icbSetgid != 0 {
		m |= fs.ModeSetgid
	}
	if in.flags&icbSticky != 0 {
		m |= fs.ModeSticky
	}
	switch in.fileType {
	case ftDirectory:
		m |= fs.ModeDir
	case ftSymlink:
		m |= fs.ModeSymlink
	case ftChar:
		m |= fs.ModeCharDevice | fs.ModeDevice
	case ftBlock:
		m |= fs.ModeDevice
	case ftFIFO:
		m |= fs.ModeNamedPipe
	case ftSocket:
		m |= fs.ModeSocket
	}
	return m
}
