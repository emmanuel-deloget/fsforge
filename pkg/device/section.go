package device

import "errors"

// Section is a bounded view onto a region of an underlying Device. It is how a
// filesystem engine is handed a single partition of a larger disk image
// without knowing anything about the surrounding container.
type Section struct {
	d         Device
	off, size int64
}

// NewSection returns the [off, off+size) window of d as its own Device.
func NewSection(d Device, off, size int64) *Section {
	return &Section{d: d, off: off, size: size}
}

func (s *Section) Size() int64 { return s.size }

func (s *Section) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off > s.size {
		return 0, errors.New("device: section read out of range")
	}
	if off+int64(len(p)) > s.size {
		p = p[:s.size-off]
	}
	return s.d.ReadAt(p, s.off+off)
}

func (s *Section) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, errors.New("device: section write out of range")
	}
	return s.d.WriteAt(p, s.off+off)
}
