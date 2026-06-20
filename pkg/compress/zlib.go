package compress

import (
	"bytes"
	"compress/zlib"
	"io"
)

// Zlib is the codec squashfs labels "gzip": despite the name, squashfs stores a
// raw zlib stream (deflate with a zlib header), not the gzip file format. It
// therefore carries the GZIP id while using compress/zlib.
type Zlib struct{}

func (Zlib) ID() uint16 { return GZIP }

func (Zlib) Compress(dst, src []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
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
