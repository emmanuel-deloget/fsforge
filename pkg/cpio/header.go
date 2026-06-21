package cpio

import (
	"fmt"
	"io/fs"
	"strconv"
)

// hdr holds the newc header fields fsforge sets or reads.
type hdr struct {
	ino       uint32
	mode      uint32
	uid       uint32
	gid       uint32
	nlink     uint32
	mtime     int64
	filesize  uint32
	devmajor  uint32
	devminor  uint32
	rdevmajor uint32
	rdevminor uint32
	namesize  uint32
}

// marshal renders the 110-byte ASCII header (magic 070701, no checksum).
func (h hdr) marshal() []byte {
	b := make([]byte, 0, headerSize)
	b = append(b, magicNewc...)
	for _, v := range []uint32{
		h.ino, h.mode, h.uid, h.gid, h.nlink, uint32(h.mtime), h.filesize,
		h.devmajor, h.devminor, h.rdevmajor, h.rdevminor, h.namesize, 0, // c_check
	} {
		b = appendHex8(b, v)
	}
	return b
}

func appendHex8(b []byte, v uint32) []byte {
	var tmp [8]byte
	s := strconv.FormatUint(uint64(v), 16)
	for i := range tmp {
		tmp[i] = '0'
	}
	copy(tmp[8-len(s):], s)
	return append(b, tmp[:]...)
}

// parseHeader reads a 110-byte newc header. It returns the parsed fields and
// whether the magic was a recognised newc variant.
func parseHeader(b []byte) (hdr, bool, error) {
	if len(b) < headerSize {
		return hdr{}, false, fmt.Errorf("cpio: short header")
	}
	magic := string(b[:6])
	if magic != magicNewc && magic != magicCRC {
		return hdr{}, false, nil
	}
	var f [13]uint32
	for i := 0; i < 13; i++ {
		v, err := strconv.ParseUint(string(b[6+i*8:6+i*8+8]), 16, 32)
		if err != nil {
			return hdr{}, false, fmt.Errorf("cpio: bad header field %d: %w", i, err)
		}
		f[i] = uint32(v)
	}
	return hdr{
		ino:       f[0],
		mode:      f[1],
		uid:       f[2],
		gid:       f[3],
		nlink:     f[4],
		mtime:     int64(f[5]),
		filesize:  f[6],
		devmajor:  f[7],
		devminor:  f[8],
		rdevmajor: f[9],
		rdevminor: f[10],
		namesize:  f[11],
	}, true, nil
}

// modeToUnix turns a Go file mode into a 32-bit Unix st_mode.
func modeToUnix(m fs.FileMode) uint32 {
	v := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		v |= sISUID
	}
	if m&fs.ModeSetgid != 0 {
		v |= sISGID
	}
	if m&fs.ModeSticky != 0 {
		v |= sISVTX
	}
	switch {
	case m&fs.ModeDir != 0:
		v |= sIFDIR
	case m&fs.ModeSymlink != 0:
		v |= sIFLNK
	case m&fs.ModeCharDevice != 0:
		v |= sIFCHR
	case m&fs.ModeDevice != 0:
		v |= sIFBLK
	case m&fs.ModeNamedPipe != 0:
		v |= sIFIFO
	case m&fs.ModeSocket != 0:
		v |= sIFSOCK
	default:
		v |= sIFREG
	}
	return v
}

// modeFromUnix is the inverse of modeToUnix.
func modeFromUnix(v uint32) fs.FileMode {
	m := fs.FileMode(v & 0o777)
	if v&sISUID != 0 {
		m |= fs.ModeSetuid
	}
	if v&sISGID != 0 {
		m |= fs.ModeSetgid
	}
	if v&sISVTX != 0 {
		m |= fs.ModeSticky
	}
	switch v & sIFMT {
	case sIFDIR:
		m |= fs.ModeDir
	case sIFLNK:
		m |= fs.ModeSymlink
	case sIFCHR:
		m |= fs.ModeCharDevice | fs.ModeDevice
	case sIFBLK:
		m |= fs.ModeDevice
	case sIFIFO:
		m |= fs.ModeNamedPipe
	case sIFSOCK:
		m |= fs.ModeSocket
	}
	return m
}
