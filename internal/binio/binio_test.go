package binio

import (
	"hash/crc32"
	"testing"
)

func TestCRC32CKnownVector(t *testing.T) {
	// crc32c of the ASCII "123456789" is a standard check value.
	const want = 0xE3069283
	if got := CRC32C([]byte("123456789")); got != want {
		t.Fatalf("CRC32C check vector = %#08x, want %#08x", got, want)
	}
}

func TestCRC32CEmpty(t *testing.T) {
	if got := CRC32C(nil); got != 0 {
		t.Fatalf("CRC32C(nil) = %#x, want 0", got)
	}
}

func TestCRC32CUpdateMatchesOneShot(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog")
	if got := CRC32CUpdate(0, data); got != CRC32C(data) {
		t.Fatalf("Update(0,data) = %#x, want %#x", got, CRC32C(data))
	}
}

func TestCRC32CUpdateSplitEqualsConcat(t *testing.T) {
	a, b := []byte("hello, "), []byte("world")
	split := CRC32CUpdate(CRC32CUpdate(0, a), b)
	whole := CRC32C(append(append([]byte{}, a...), b...))
	if split != whole {
		t.Fatalf("split %#x != whole %#x", split, whole)
	}
}

func TestCRC32CMatchesStdlibCastagnoli(t *testing.T) {
	data := []byte("fsforge")
	want := crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli))
	if got := CRC32C(data); got != want {
		t.Fatalf("CRC32C = %#x, stdlib = %#x", got, want)
	}
}
