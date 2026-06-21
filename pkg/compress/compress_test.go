package compress

import (
	"bytes"
	"testing"
)

// codecs lists every Compressor the package ships; each must round-trip.
func codecs() []Compressor { return []Compressor{Gzip{}, Zlib{}} }

func TestRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("fsforge compresses blocks independently. "), 32)
	for _, c := range codecs() {
		comp, err := c.Compress(nil, payload)
		if err != nil {
			t.Fatalf("%T Compress: %v", c, err)
		}
		got, err := c.Decompress(nil, comp)
		if err != nil {
			t.Fatalf("%T Decompress: %v", c, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("%T round-trip mismatch", c)
		}
	}
}

func TestCompressAppendsToDst(t *testing.T) {
	prefix := []byte("PREFIX")
	c := Gzip{}
	out, err := c.Compress(append([]byte{}, prefix...), []byte("body"))
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if !bytes.HasPrefix(out, prefix) {
		t.Fatalf("Compress did not preserve dst prefix: %q", out)
	}
	got, err := c.Decompress(append([]byte{}, prefix...), out[len(prefix):])
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if string(got) != "PREFIXbody" {
		t.Fatalf("Decompress appended = %q, want \"PREFIXbody\"", got)
	}
}

func TestDecompressBadInput(t *testing.T) {
	if _, err := (Gzip{}).Decompress(nil, []byte("not gzip")); err == nil {
		t.Fatal("Decompress of garbage should fail")
	}
	if _, err := (Zlib{}).Decompress(nil, []byte("not zlib")); err == nil {
		t.Fatal("Decompress of garbage should fail")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get(GZIP); ok {
		t.Fatal("empty registry should not resolve GZIP")
	}
	r.Register(Zlib{})
	c, ok := r.Get(GZIP) // Zlib carries the GZIP id
	if !ok {
		t.Fatal("registered codec not found")
	}
	if c.ID() != GZIP {
		t.Fatalf("ID = %d, want %d", c.ID(), GZIP)
	}
	if _, ok := r.Get(ZSTD); ok {
		t.Fatal("unregistered ZSTD should not resolve")
	}
}

func TestGzipID(t *testing.T) {
	if (Gzip{}).ID() != GZIP {
		t.Fatalf("Gzip ID = %d, want %d", Gzip{}.ID(), GZIP)
	}
}
