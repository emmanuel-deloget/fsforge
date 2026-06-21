package romfs

import "io/fs"

// checksum returns the value that makes the big-endian 32-bit words of data sum
// to zero (romfs stores it in the header/superblock checksum field). data must
// be a whole number of 32-bit words.
func checksum(data []byte) uint32 {
	var sum uint32
	for off := 0; off+4 <= len(data); off += 4 {
		sum += be.Uint32(data[off:])
	}
	return -sum
}

// fixChecksum zeroes the 4-byte checksum field at csumOff, then stores the value
// that makes the words of data[:n] sum to zero.
func fixChecksum(data []byte, csumOff, n int) {
	be.PutUint32(data[csumOff:], 0)
	be.PutUint32(data[csumOff:], checksum(data[:n]))
}

// putHeader writes a 16-byte romfs file header (next/spec/size/checksum) at the
// start of dst, with the type/exec flags folded into next's low bits. The
// checksum is left zero; the caller fixes it once the name is in place.
func putHeader(dst []byte, nextOff uint32, flags uint32, spec, size uint32) {
	be.PutUint32(dst[0:], (nextOff&alignMask)|flags)
	be.PutUint32(dst[4:], spec)
	be.PutUint32(dst[8:], size)
	be.PutUint32(dst[12:], 0) // checksum, filled later
}

// typeOf maps a node mode onto a romfs file type and whether the exec flag is
// set (directories are always searchable; regular files when any execute bit is
// present).
func typeOf(m fs.FileMode) (uint32, bool) {
	switch {
	case m&fs.ModeDir != 0:
		return typeDir, true
	case m&fs.ModeSymlink != 0:
		return typeSymlink, false
	case m&fs.ModeCharDevice != 0:
		return typeChar, false
	case m&fs.ModeDevice != 0:
		return typeBlock, false
	case m&fs.ModeNamedPipe != 0:
		return typeFifo, false
	case m&fs.ModeSocket != 0:
		return typeSocket, false
	default:
		return typeReg, m.Perm()&0o111 != 0
	}
}

// modeOf reconstructs a Go file mode from a romfs type and exec flag. romfs
// keeps no permission bits, so conventional defaults are applied.
func modeOf(typ uint32, exec bool) fs.FileMode {
	switch typ {
	case typeDir:
		return fs.ModeDir | 0o755
	case typeSymlink:
		return fs.ModeSymlink | 0o777
	case typeChar:
		return fs.ModeCharDevice | fs.ModeDevice | 0o644
	case typeBlock:
		return fs.ModeDevice | 0o644
	case typeFifo:
		return fs.ModeNamedPipe | 0o644
	case typeSocket:
		return fs.ModeSocket | 0o644
	default:
		if exec {
			return 0o755
		}
		return 0o644
	}
}
