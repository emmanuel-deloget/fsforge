package qcow2

import (
	"errors"
	"io"
	"sort"
)

// File is the host-file capability the Writer needs: random IO plus Truncate.
// *os.File satisfies it.
type File interface {
	io.ReaderAt
	io.WriterAt
	Truncate(size int64) error
}

// Writer is a writable QCOW2 image presented as a fixed-size device. Engines
// write to the virtual address space; data clusters are allocated lazily and
// streamed to the host file, and the L1/L2/refcount metadata is materialised by
// Finalize. The zero value is not usable; start from NewWriter.
type Writer struct {
	f    File
	size int64 // virtual disk size

	l1Size   int64
	l2map    map[int64]int64 // guest cluster -> host cluster index (>=1)
	nextHost int64           // next free host cluster index (cluster 0 = header)

	finalized bool
}

// NewWriter returns a writable QCOW2 device of virtualSize bytes backed by f.
// Nothing is written until the first WriteAt; Finalize flushes the metadata.
func NewWriter(f File, virtualSize int64) (*Writer, error) {
	if virtualSize <= 0 {
		return nil, errors.New("qcow2: virtual size must be positive")
	}
	clustersPerL1 := int64(clusterSize) * l2Entries
	return &Writer{
		f:        f,
		size:     virtualSize,
		l1Size:   ceilDiv(virtualSize, clustersPerL1),
		l2map:    make(map[int64]int64),
		nextHost: 1,
	}, nil
}

// Size reports the virtual disk size.
func (w *Writer) Size() int64 { return w.size }

// WriteAt stores p at the virtual offset off, allocating clusters as needed. A
// not-yet-allocated cluster whose written bytes are all zero is left
// unallocated, keeping the image sparse.
func (w *Writer) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > w.size {
		return 0, errors.New("qcow2: write out of range")
	}
	written := 0
	for len(p) > 0 {
		gc := off / clusterSize
		inCl := off % clusterSize
		n := clusterSize - inCl
		if int64(len(p)) < n {
			n = int64(len(p))
		}
		chunk := p[:n]

		host, ok := w.l2map[gc]
		if !ok {
			if allZero(chunk) {
				written += len(chunk)
				off += n
				p = p[n:]
				continue
			}
			host = w.nextHost
			w.nextHost++
			w.l2map[gc] = host
		}
		if _, err := w.f.WriteAt(chunk, host*clusterSize+inCl); err != nil {
			return written, err
		}
		written += len(chunk)
		off += n
		p = p[n:]
	}
	return written, nil
}

// ReadAt returns previously written bytes, with unallocated regions and the
// unwritten remainder of a partially written cluster reading as zeros.
func (w *Writer) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("qcow2: negative offset")
	}
	if off >= w.size {
		return 0, io.EOF
	}
	total := len(p)
	for i := range p {
		p[i] = 0
	}
	for n := 0; n < total; {
		at := off + int64(n)
		if at >= w.size {
			return n, io.EOF
		}
		gc := at / clusterSize
		inCl := at % clusterSize
		span := clusterSize - inCl
		if rem := int64(total - n); span > rem {
			span = rem
		}
		if host, ok := w.l2map[gc]; ok {
			// Tolerate EOF: the cluster may be only partially backed on disk.
			if _, err := w.f.ReadAt(p[n:int64(n)+span], host*clusterSize+inCl); err != nil && err != io.EOF {
				return n, err
			}
		}
		n += int(span)
	}
	return total, nil
}

// Finalize writes the L2 tables, L1 table, refcount structures and header, then
// truncates the host file to the allocated size. After Finalize the writer must
// not be used again.
func (w *Writer) Finalize() error {
	if w.finalized {
		return errors.New("qcow2: already finalized")
	}
	w.finalized = true

	l1 := make([]uint64, w.l1Size)
	if err := w.writeL2Tables(l1); err != nil {
		return err
	}

	l1Off := w.nextHost * clusterSize
	l1Clusters := ceilDiv(w.l1Size*8, clusterSize)
	w.nextHost += l1Clusters
	if err := w.writeTable(l1, l1Off); err != nil {
		return err
	}

	refTableOff, refTableClusters, err := w.writeRefcounts()
	if err != nil {
		return err
	}

	h := header{
		version:          version3,
		clusterBits:      clusterBits,
		size:             uint64(w.size),
		l1Size:           uint32(w.l1Size),
		l1TableOffset:    uint64(l1Off),
		refTableOffset:   uint64(refTableOff),
		refTableClusters: uint32(refTableClusters),
		refcountOrder:    refcountOrder,
	}
	if _, err := w.f.WriteAt(h.marshal(), 0); err != nil {
		return err
	}
	return w.f.Truncate(w.nextHost * clusterSize)
}

// writeL2Tables allocates and writes one L2 table per populated L1 slot,
// filling l1 with the corresponding entries.
func (w *Writer) writeL2Tables(l1 []uint64) error {
	// Group guest clusters by their L1 index, deterministically.
	byL1 := map[int64][]int64{}
	for gc := range w.l2map {
		idx := gc / l2Entries
		byL1[idx] = append(byL1[idx], gc)
	}
	idxs := make([]int64, 0, len(byL1))
	for idx := range byL1 {
		idxs = append(idxs, idx)
	}
	sort.Slice(idxs, func(i, j int) bool { return idxs[i] < idxs[j] })

	for _, idx := range idxs {
		l2 := make([]uint64, l2Entries)
		for _, gc := range byL1[idx] {
			l2[gc%l2Entries] = uint64(w.l2map[gc]*clusterSize) | flagCopied
		}
		off := w.nextHost * clusterSize
		w.nextHost++
		if err := w.writeTable(l2, off); err != nil {
			return err
		}
		l1[idx] = uint64(off) | flagCopied
	}
	return nil
}

// writeRefcounts computes, builds and writes the refcount table and blocks so
// that every allocated cluster (including the refcount structures themselves)
// has refcount 1. It returns the refcount table offset and its cluster count.
func (w *Writer) writeRefcounts() (int64, int64, error) {
	used := w.nextHost
	rbCount, rtClusters := int64(1), int64(1)
	for {
		total := used + rtClusters + rbCount
		nrb := ceilDiv(total, refcountsPerCl)
		nrt := ceilDiv(nrb*8, clusterSize)
		if nrb == rbCount && nrt == rtClusters {
			break
		}
		rbCount, rtClusters = nrb, nrt
	}

	rtOff := w.nextHost * clusterSize
	w.nextHost += rtClusters
	rbOff := w.nextHost * clusterSize
	w.nextHost += rbCount
	total := w.nextHost // every cluster in [0,total) has refcount 1

	blocks := make([]byte, rbCount*clusterSize)
	for c := int64(0); c < total; c++ {
		be.PutUint16(blocks[c*2:], 1)
	}
	if _, err := w.f.WriteAt(blocks, rbOff); err != nil {
		return 0, 0, err
	}

	table := make([]byte, rtClusters*clusterSize)
	for b := int64(0); b < rbCount; b++ {
		be.PutUint64(table[b*8:], uint64(rbOff+b*clusterSize))
	}
	if _, err := w.f.WriteAt(table, rtOff); err != nil {
		return 0, 0, err
	}
	return rtOff, rtClusters, nil
}

// writeTable writes a slice of u64 entries (an L1 or L2 table) big-endian at off.
func (w *Writer) writeTable(entries []uint64, off int64) error {
	b := make([]byte, len(entries)*8)
	for i, e := range entries {
		be.PutUint64(b[i*8:], e)
	}
	_, err := w.f.WriteAt(b, off)
	return err
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
