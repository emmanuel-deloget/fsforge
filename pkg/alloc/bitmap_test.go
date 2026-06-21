package alloc

import (
	"errors"
	"testing"
)

func TestBitmapAllocContiguous(t *testing.T) {
	b := NewBitmap(64)
	start, err := b.Alloc(4)
	if err != nil {
		t.Fatalf("Alloc(4): %v", err)
	}
	if start != 0 {
		t.Fatalf("first run start = %d, want 0", start)
	}
	start, err = b.Alloc(2)
	if err != nil {
		t.Fatalf("Alloc(2): %v", err)
	}
	if start != 4 {
		t.Fatalf("second run start = %d, want 4", start)
	}
}

func TestBitmapAllocZero(t *testing.T) {
	b := NewBitmap(8)
	if _, err := b.Alloc(0); err == nil {
		t.Fatal("Alloc(0) should fail")
	}
}

func TestBitmapNoSpace(t *testing.T) {
	b := NewBitmap(4)
	if _, err := b.Alloc(8); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc beyond capacity: got %v, want ErrNoSpace", err)
	}
}

func TestBitmapFreeReuse(t *testing.T) {
	b := NewBitmap(16)
	a, _ := b.Alloc(4) // [0,4)
	_, _ = b.Alloc(4)  // [4,8)
	if err := b.Free(a, 4); err != nil {
		t.Fatalf("Free: %v", err)
	}
	// First-fit must reuse the freed lowest run.
	got, err := b.Alloc(4)
	if err != nil {
		t.Fatalf("Alloc after free: %v", err)
	}
	if got != 0 {
		t.Fatalf("reused start = %d, want 0", got)
	}
}

func TestBitmapFragmentation(t *testing.T) {
	b := NewBitmap(12) // fully carved into three 4-runs, no free tail
	r0, _ := b.Alloc(4) // [0,4)
	_, _ = b.Alloc(4)   // [4,8)
	r2, _ := b.Alloc(4) // [8,12)
	b.Free(r0, 4)
	b.Free(r2, 4)
	// The two 4-wide holes are separated by the still-used [4,8); a 5-run fits
	// in neither and there is no tail, so allocation must fail.
	if _, err := b.Alloc(5); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc(5) across fragmentation: got %v, want ErrNoSpace", err)
	}
	// A 4-run fits the first hole.
	if got, _ := b.Alloc(4); got != 0 {
		t.Fatalf("Alloc(4) into first hole = %d, want 0", got)
	}
}

func TestBitmapReserve(t *testing.T) {
	b := NewBitmap(16)
	if err := b.Reserve(0, 4); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	// Reserved region must be skipped by Alloc.
	if got, _ := b.Alloc(4); got != 4 {
		t.Fatalf("Alloc after reserve = %d, want 4", got)
	}
}

func TestBitmapOutOfRange(t *testing.T) {
	b := NewBitmap(8)
	if err := b.Free(4, 8); err == nil {
		t.Fatal("Free out of range should fail")
	}
	if err := b.Reserve(4, 8); err == nil {
		t.Fatal("Reserve out of range should fail")
	}
}

func TestBitmapDeterministic(t *testing.T) {
	seq := []uint64{3, 1, 5, 2}
	run := func() []uint64 {
		b := NewBitmap(64)
		var starts []uint64
		for _, n := range seq {
			s, err := b.Alloc(n)
			if err != nil {
				t.Fatalf("Alloc(%d): %v", n, err)
			}
			starts = append(starts, s)
		}
		return starts
	}
	a, c := run(), run()
	for i := range a {
		if a[i] != c[i] {
			t.Fatalf("non-deterministic: run1=%v run2=%v", a, c)
		}
	}
}

func TestBitmapFactory(t *testing.T) {
	var f Factory = BitmapFactory{}
	a := f.New(32)
	if _, err := a.Alloc(1); err != nil {
		t.Fatalf("factory allocator Alloc: %v", err)
	}
}
