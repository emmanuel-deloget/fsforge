package device

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestMemReadWrite(t *testing.T) {
	m := NewMem(16)
	if m.Size() != 16 {
		t.Fatalf("Size = %d, want 16", m.Size())
	}
	if n, err := m.WriteAt([]byte("hello"), 2); err != nil || n != 5 {
		t.Fatalf("WriteAt = (%d, %v)", n, err)
	}
	p := make([]byte, 5)
	if n, err := m.ReadAt(p, 2); err != nil || n != 5 || string(p) != "hello" {
		t.Fatalf("ReadAt = (%q, %d, %v)", p, n, err)
	}
}

func TestMemReadAtEOF(t *testing.T) {
	m := NewMem(4)
	p := make([]byte, 8)
	n, err := m.ReadAt(p, 0)
	if !errors.Is(err, io.EOF) || n != 4 {
		t.Fatalf("ReadAt short = (%d, %v), want (4, EOF)", n, err)
	}
	if _, err := m.ReadAt(p, 4); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt at end err = %v, want EOF", err)
	}
	if _, err := m.ReadAt(p, -1); err == nil {
		t.Fatal("ReadAt(-1) should fail")
	}
}

func TestMemWriteAtBounds(t *testing.T) {
	m := NewMem(4)
	if _, err := m.WriteAt([]byte("toolong!!"), 0); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("overflowing WriteAt err = %v, want ErrShortWrite", err)
	}
	if _, err := m.WriteAt([]byte("x"), -1); err == nil {
		t.Fatal("WriteAt(-1) should fail")
	}
}

func TestMemDiscard(t *testing.T) {
	m := NewMem(8)
	m.WriteAt([]byte("ABCDEFGH"), 0)
	if err := m.Discard(2, 3); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	want := []byte("AB\x00\x00\x00FGH")
	if !bytes.Equal(m.Bytes(), want) {
		t.Fatalf("after Discard = %q, want %q", m.Bytes(), want)
	}
	if err := m.Discard(6, 4); err == nil {
		t.Fatal("Discard out of range should fail")
	}
}

func TestMemIsDiscarder(t *testing.T) {
	var d Device = NewMem(8)
	if _, ok := d.(Discarder); !ok {
		t.Fatal("Mem should implement Discarder")
	}
}

func TestSectionWindow(t *testing.T) {
	base := NewMem(32)
	base.WriteAt(bytes.Repeat([]byte{0xff}, 32), 0)
	sec := NewSection(base, 8, 16)
	if sec.Size() != 16 {
		t.Fatalf("Size = %d, want 16", sec.Size())
	}
	if n, err := sec.WriteAt([]byte("data"), 0); err != nil || n != 4 {
		t.Fatalf("WriteAt = (%d, %v)", n, err)
	}
	// The write must land at absolute offset 8 in the parent.
	p := make([]byte, 4)
	base.ReadAt(p, 8)
	if string(p) != "data" {
		t.Fatalf("parent at 8 = %q, want \"data\"", p)
	}
	sec.ReadAt(p, 0)
	if string(p) != "data" {
		t.Fatalf("section read = %q, want \"data\"", p)
	}
}

func TestSectionReadClamp(t *testing.T) {
	base := NewMem(32)
	base.WriteAt(bytes.Repeat([]byte{0xaa}, 32), 0)
	sec := NewSection(base, 8, 8)
	// Read past the section end is clamped to the window.
	p := make([]byte, 16)
	n, _ := sec.ReadAt(p, 4)
	if n != 4 {
		t.Fatalf("clamped read n = %d, want 4", n)
	}
}

func TestSectionOutOfRange(t *testing.T) {
	sec := NewSection(NewMem(32), 8, 8)
	if _, err := sec.ReadAt(make([]byte, 1), -1); err == nil {
		t.Fatal("section ReadAt(-1) should fail")
	}
	if _, err := sec.WriteAt(make([]byte, 4), 8); err == nil {
		t.Fatal("section WriteAt past end should fail")
	}
	if _, err := sec.WriteAt(make([]byte, 1), -1); err == nil {
		t.Fatal("section WriteAt(-1) should fail")
	}
}

func TestFileBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(64); err != nil {
		t.Fatal(err)
	}
	dev := NewFile(f, 64)
	if dev.Size() != 64 {
		t.Fatalf("Size = %d, want 64", dev.Size())
	}
	if _, err := dev.WriteAt([]byte("forge"), 10); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	p := make([]byte, 5)
	if _, err := dev.ReadAt(p, 10); err != nil || string(p) != "forge" {
		t.Fatalf("ReadAt = (%q, %v)", p, err)
	}
}

// Compile-time: all three backends satisfy Device.
var (
	_ Device = (*Mem)(nil)
	_ Device = (*Section)(nil)
	_ Device = (*File)(nil)
)
