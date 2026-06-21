package qcow2

import (
	"errors"
	"fmt"
	"io"
)

// Reader presents an existing QCOW2 image as a read-only, fixed-size device.
// It maps virtual offsets through the L1/L2 tables to host clusters; only
// uncompressed clusters are supported.
type Reader struct {
	f           io.ReaderAt
	size        int64
	clusterSize int64
	l2Entries   int64
	l1          []uint64
	l2cache     map[int64][]uint64 // L1 index -> decoded L2 table
}

// IsQcow2 reports whether b begins with the QCOW2 magic.
func IsQcow2(b []byte) bool {
	return len(b) >= 4 && be.Uint32(b) == magic
}

// Open parses the QCOW2 image in f and returns it as a read-only device.
func Open(f io.ReaderAt) (*Reader, error) {
	head := make([]byte, headerLen)
	if _, err := f.ReadAt(head, 0); err != nil && err != io.EOF {
		return nil, err
	}
	h, err := parseHeader(head)
	if err != nil {
		return nil, err
	}
	cs := int64(1) << h.clusterBits
	r := &Reader{
		f:           f,
		size:        int64(h.size),
		clusterSize: cs,
		l2Entries:   cs / 8,
		l1:          make([]uint64, h.l1Size),
		l2cache:     map[int64][]uint64{},
	}
	raw := make([]byte, int64(h.l1Size)*8)
	if _, err := f.ReadAt(raw, int64(h.l1TableOffset)); err != nil && err != io.EOF {
		return nil, err
	}
	for i := range r.l1 {
		r.l1[i] = be.Uint64(raw[i*8:])
	}
	return r, nil
}

// Size reports the virtual disk size.
func (r *Reader) Size() int64 { return r.size }

// WriteAt rejects writes: an opened image is read-only.
func (r *Reader) WriteAt([]byte, int64) (int, error) {
	return 0, errors.New("qcow2: image opened read-only")
}

// ReadAt resolves p at virtual offset off, returning zeros for unallocated
// clusters.
func (r *Reader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("qcow2: negative offset")
	}
	if off >= r.size {
		return 0, io.EOF
	}
	for i := range p {
		p[i] = 0
	}
	total := len(p)
	for n := 0; n < total; {
		at := off + int64(n)
		if at >= r.size {
			return n, io.EOF
		}
		gc := at / r.clusterSize
		inCl := at % r.clusterSize
		span := r.clusterSize - inCl
		if rem := int64(total - n); span > rem {
			span = rem
		}
		host, err := r.hostOffset(gc)
		if err != nil {
			return n, err
		}
		if host >= 0 {
			if _, err := r.f.ReadAt(p[n:int64(n)+span], host+inCl); err != nil && err != io.EOF {
				return n, err
			}
		}
		n += int(span)
	}
	return total, nil
}

// hostOffset returns the host byte offset backing guest cluster gc, or -1 when
// the cluster is unallocated or reads as zeros.
func (r *Reader) hostOffset(gc int64) (int64, error) {
	l1idx := gc / r.l2Entries
	if l1idx >= int64(len(r.l1)) {
		return -1, nil
	}
	l1e := r.l1[l1idx]
	if l1e&offsetMask == 0 {
		return -1, nil
	}
	l2, err := r.l2table(l1idx, int64(l1e&offsetMask))
	if err != nil {
		return -1, err
	}
	l2e := l2[gc%r.l2Entries]
	if l2e&flagCompressed != 0 {
		return -1, fmt.Errorf("qcow2: compressed clusters are not supported")
	}
	if l2e&flagZero != 0 || l2e&offsetMask == 0 {
		return -1, nil
	}
	return int64(l2e & offsetMask), nil
}

func (r *Reader) l2table(l1idx, off int64) ([]uint64, error) {
	if t, ok := r.l2cache[l1idx]; ok {
		return t, nil
	}
	raw := make([]byte, r.clusterSize)
	if _, err := r.f.ReadAt(raw, off); err != nil && err != io.EOF {
		return nil, err
	}
	t := make([]uint64, r.l2Entries)
	for i := range t {
		t[i] = be.Uint64(raw[i*8:])
	}
	r.l2cache[l1idx] = t
	return t, nil
}
