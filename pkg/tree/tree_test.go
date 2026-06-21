package tree

import (
	"errors"
	"io"
	"testing"
)

func TestBytesSize(t *testing.T) {
	if got := Bytes("hello").Size(); got != 5 {
		t.Fatalf("Size = %d, want 5", got)
	}
}

func TestBytesReadAtFull(t *testing.T) {
	b := Bytes("abcdef")
	p := make([]byte, 3)
	n, err := b.ReadAt(p, 1)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != 3 || string(p) != "bcd" {
		t.Fatalf("ReadAt = %q (n=%d), want \"bcd\"", p, n)
	}
}

func TestBytesReadAtShortEOF(t *testing.T) {
	b := Bytes("abc")
	p := make([]byte, 8)
	n, err := b.ReadAt(p, 1)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if n != 2 || string(p[:n]) != "bc" {
		t.Fatalf("ReadAt = %q (n=%d), want \"bc\"", p[:n], n)
	}
}

func TestBytesReadAtAtEnd(t *testing.T) {
	b := Bytes("abc")
	if _, err := b.ReadAt(make([]byte, 1), 3); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt at end: err = %v, want io.EOF", err)
	}
}

func TestBytesReadAtNegative(t *testing.T) {
	b := Bytes("abc")
	if _, err := b.ReadAt(make([]byte, 1), -1); err == nil {
		t.Fatal("ReadAt(-1) should fail")
	}
}

// Bytes must satisfy Source.
var _ Source = Bytes(nil)
