package device

import (
	"errors"
	"io"
)

// Mem is an in-memory Device. It is the primary test backend: engines run
// against it and assertions are made on the resulting bytes without touching
// the host filesystem or requiring privileges.
type Mem struct {
	data []byte
}

// NewMem returns a zeroed in-memory device of the given size.
func NewMem(size int64) *Mem {
	return &Mem{data: make([]byte, size)}
}

// Bytes exposes the backing buffer for golden-image comparisons in tests.
func (m *Mem) Bytes() []byte { return m.data }

func (m *Mem) Size() int64 { return int64(len(m.data)) }

func (m *Mem) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("device: negative offset")
	}
	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *Mem) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("device: negative offset")
	}
	if off+int64(len(p)) > int64(len(m.data)) {
		return 0, io.ErrShortWrite
	}
	return copy(m.data[off:], p), nil
}

// Discard implements Discarder by zeroing the range.
func (m *Mem) Discard(off, length int64) error {
	if off < 0 || off+length > int64(len(m.data)) {
		return errors.New("device: discard out of range")
	}
	clear(m.data[off : off+length])
	return nil
}
