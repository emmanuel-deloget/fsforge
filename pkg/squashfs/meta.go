package squashfs

import (
	"encoding/binary"

	"github.com/emmanuel-deloget/fsforge/pkg/compress"
)

// metaWriter accumulates a squashfs metadata stream (inode table, directory
// table, …) as a sequence of compressed 8 KiB blocks. It exposes ref() so a
// structure's location can be recorded before it is written, which is how
// directory entries and inodes cross-reference each other.
type metaWriter struct {
	comp    compress.Compressor
	buf     []byte // pending uncompressed bytes (< metaBlockSize after each write)
	out     []byte // emitted compressed stream
	emitted uint32 // len(out): start offset of the current (unflushed) block
}

// ref returns the location the next written byte will occupy: the compressed
// start of its metadata block and the offset within the uncompressed block.
func (m *metaWriter) ref() (uint32, uint16) {
	return m.emitted, uint16(len(m.buf))
}

func (m *metaWriter) write(p []byte) {
	m.buf = append(m.buf, p...)
	for len(m.buf) >= metaBlockSize {
		m.flush(metaBlockSize)
	}
}

func (m *metaWriter) finish() {
	if len(m.buf) > 0 {
		m.flush(len(m.buf))
	}
}

func (m *metaWriter) flush(n int) {
	block := metaBlock(m.comp, m.buf[:n])
	m.out = append(m.out, block...)
	m.emitted += uint32(len(block))
	m.buf = m.buf[n:]
}

// metaBlock encodes one metadata block: a 2-byte header (length + uncompressed
// flag) followed by the payload, stored uncompressed when compression does not
// help.
func metaBlock(comp compress.Compressor, data []byte) []byte {
	c, err := comp.Compress(nil, data)
	var payload []byte
	var hdr uint16
	if err == nil && len(c) < len(data) {
		payload, hdr = c, uint16(len(c))
	} else {
		payload, hdr = data, uint16(len(data))|metaUncompressed
	}
	out := make([]byte, 2+len(payload))
	binary.LittleEndian.PutUint16(out, hdr)
	copy(out[2:], payload)
	return out
}

// dataBlock compresses a file data block, returning the bytes to store and the
// size field for the inode (with the uncompressed marker when stored raw).
func dataBlock(comp compress.Compressor, data []byte) ([]byte, uint32) {
	c, err := comp.Compress(nil, data)
	if err == nil && len(c) < len(data) {
		return c, uint32(len(c))
	}
	return data, uint32(len(data)) | blockUncompressed
}
