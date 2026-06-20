// Package compress defines the block-compression contract used by engines such
// as squashfs and erofs, plus a registry and pure-Go adapters. Compressors are
// injected so an engine never hard-depends on a specific codec, and so tests
// can substitute a trivial one.
package compress

import (
	"bytes"
	"compress/gzip"
	"io"
)

// Compressor compresses and decompresses independent blocks. ID is the on-disk
// identifier the container format records (the constants below follow squashfs).
type Compressor interface {
	ID() uint16
	// Compress appends the compressed form of src to dst and returns dst.
	Compress(dst, src []byte) ([]byte, error)
	// Decompress appends the decompressed form of src to dst and returns dst.
	Decompress(dst, src []byte) ([]byte, error)
}

// squashfs compressor identifiers.
const (
	GZIP uint16 = 1
	LZMA uint16 = 2
	LZO  uint16 = 3
	XZ   uint16 = 4
	LZ4  uint16 = 5
	ZSTD uint16 = 6
)

// Registry maps compressor IDs to implementations, populated by the caller so
// that only the codecs actually needed are linked in.
type Registry struct{ m map[uint16]Compressor }

func NewRegistry() *Registry { return &Registry{m: make(map[uint16]Compressor)} }

func (r *Registry) Register(c Compressor) { r.m[c.ID()] = c }

func (r *Registry) Get(id uint16) (Compressor, bool) {
	c, ok := r.m[id]
	return c, ok
}

// Gzip is the stdlib-backed gzip codec; it pulls in no external dependency.
// zstd, lz4 and xz adapters live in their own files and wrap pure-Go libraries.
type Gzip struct{}

func (Gzip) ID() uint16 { return GZIP }

func (Gzip) Compress(dst, src []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(src); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return append(dst, buf.Bytes()...), nil
}

func (Gzip) Decompress(dst, src []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(src))
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
