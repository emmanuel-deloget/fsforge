package compress

import (
	"bytes"
	"compress/zlib"
	"io"
	"sync"
)

// Zlib is the codec squashfs labels "gzip": despite the name, squashfs stores a
// raw zlib stream (deflate with a zlib header), not the gzip file format. It
// therefore carries the GZIP id while using compress/zlib.
type Zlib struct{}

func (Zlib) ID() uint16 { return GZIP }

// writerPool keeps deflate state alive between blocks. zlib.NewWriter allocates
// its hash tables and window on every call — around 800 KiB — and a squashfs
// image is thousands of blocks, so building one was spending most of its
// allocation budget on state it threw away immediately. Resetting a pooled
// writer keeps the tables and costs nothing.
var writerPool = sync.Pool{
	New: func() any { return zlib.NewWriter(io.Discard) },
}

func (Zlib) Compress(dst, src []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := writerPool.Get().(*zlib.Writer)
	defer writerPool.Put(w)
	w.Reset(&buf)

	if _, err := w.Write(src); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return append(dst, buf.Bytes()...), nil
}

func (Zlib) Decompress(dst, src []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return append(dst, out...), nil
}
