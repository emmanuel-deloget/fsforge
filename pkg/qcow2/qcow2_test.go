package qcow2

import (
	"bytes"
	"io"
	"testing"
)

// memFile is an in-memory File for tests.
type memFile struct{ b []byte }

func (m *memFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m.b)) {
		return 0, io.EOF
	}
	n := copy(p, m.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *memFile) WriteAt(p []byte, off int64) (int, error) {
	end := off + int64(len(p))
	if end > int64(len(m.b)) {
		m.b = append(m.b, make([]byte, end-int64(len(m.b)))...)
	}
	copy(m.b[off:], p)
	return len(p), nil
}

func (m *memFile) Truncate(n int64) error {
	if n <= int64(len(m.b)) {
		m.b = m.b[:n]
	} else {
		m.b = append(m.b, make([]byte, n-int64(len(m.b)))...)
	}
	return nil
}

const virtual = 8 << 20 // 8 MiB virtual disk

// writePattern writes recognisable data at a few scattered offsets, mirroring
// how a partition table touches both ends of the disk.
func writePattern(t *testing.T, w *Writer) map[int64][]byte {
	t.Helper()
	regions := map[int64][]byte{
		0:               bytes.Repeat([]byte("MBR!"), 128),       // first sector area
		1024:            []byte("superblock-ish payload"),        // mid-cluster
		clusterSize - 8: []byte("ACROSSBND"),                     // straddles a cluster boundary
		3 * clusterSize: bytes.Repeat([]byte{0xAB}, clusterSize), // a full cluster
		virtual - 512:   bytes.Repeat([]byte("END."), 128),       // backup GPT area
	}
	for off, data := range regions {
		if _, err := w.WriteAt(data, off); err != nil {
			t.Fatalf("WriteAt %d: %v", off, err)
		}
	}
	return regions
}

func TestWriteReadRoundTrip(t *testing.T) {
	mf := &memFile{}
	w, err := NewWriter(mf, virtual)
	if err != nil {
		t.Fatal(err)
	}
	regions := writePattern(t, w)

	// Read back through the same Writer before finalizing.
	for off, want := range regions {
		got := make([]byte, len(want))
		if _, err := w.ReadAt(got, off); err != nil {
			t.Fatalf("Writer.ReadAt %d: %v", off, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("Writer.ReadAt %d mismatch", off)
		}
	}
	// A never-written region reads as zeros.
	z := make([]byte, 64)
	w.ReadAt(z, 2*clusterSize)
	if !allZero(z) {
		t.Errorf("unwritten region not zero")
	}

	if err := w.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Reopen with the Reader and verify the same bytes plus zero holes.
	r, err := Open(mf)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if r.Size() != virtual {
		t.Errorf("size = %d", r.Size())
	}
	for off, want := range regions {
		got := make([]byte, len(want))
		if _, err := r.ReadAt(got, off); err != nil {
			t.Fatalf("Reader.ReadAt %d: %v", off, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("Reader.ReadAt %d mismatch:\n got %q\nwant %q", off, got, want)
		}
	}
	z2 := make([]byte, clusterSize)
	r.ReadAt(z2, 5*clusterSize)
	if !allZero(z2) {
		t.Errorf("unallocated cluster not zero on read-back")
	}
}

func TestSparse(t *testing.T) {
	mf := &memFile{}
	w, _ := NewWriter(mf, 1<<30) // 1 GiB virtual
	// Write only two clusters' worth of data far apart.
	w.WriteAt([]byte("hello"), 0)
	w.WriteAt([]byte("world"), 900<<20)
	if err := w.Finalize(); err != nil {
		t.Fatal(err)
	}
	// The host file must be a tiny fraction of the 1 GiB virtual size.
	if got := int64(len(mf.b)); got > 2<<20 {
		t.Errorf("sparse image too large: %d bytes for 1 GiB virtual", got)
	}
}

func TestReproducible(t *testing.T) {
	build := func() []byte {
		mf := &memFile{}
		w, _ := NewWriter(mf, virtual)
		writePattern(t, w)
		if err := w.Finalize(); err != nil {
			t.Fatal(err)
		}
		return mf.b
	}
	if !bytes.Equal(build(), build()) {
		t.Fatal("identical writes produced different qcow2 images")
	}
}

func TestHeaderShape(t *testing.T) {
	mf := &memFile{}
	w, _ := NewWriter(mf, virtual)
	w.WriteAt([]byte("x"), 0)
	w.Finalize()
	h, err := parseHeader(mf.b[:headerLen])
	if err != nil {
		t.Fatalf("parseHeader: %v", err)
	}
	if h.version != version3 || h.clusterBits != clusterBits {
		t.Errorf("version=%d clusterBits=%d", h.version, h.clusterBits)
	}
	if h.size != virtual || h.l1Size == 0 || h.l1TableOffset == 0 || h.refTableOffset == 0 {
		t.Errorf("bad header: %+v", h)
	}
}
