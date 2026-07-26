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
	b := NewBitmap(12)  // fully carved into three 4-runs, no free tail
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

func TestBitmapAllocUpTo(t *testing.T) {
	b := NewBitmap(16)
	_ = b.Reserve(6, 10) // only [0,6) is free
	start, got, err := b.AllocUpTo(10)
	if err != nil {
		t.Fatalf("AllocUpTo: %v", err)
	}
	if start != 0 || got != 6 {
		t.Fatalf("AllocUpTo(10) = (%d, %d), want (0, 6)", start, got)
	}
	if _, _, err := b.AllocUpTo(1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("AllocUpTo on a full bitmap: got %v, want ErrNoSpace", err)
	}
}

// AllocRuns is the reason large files can be laid out at all: it must chain
// several runs when no single one is long enough.
func TestAllocRunsChainsHoles(t *testing.T) {
	b := NewBitmap(32)
	_ = b.Reserve(4, 1)  // three holes: [0,4), [5,20), [21,32)
	_ = b.Reserve(20, 1) //
	runs, err := AllocRuns(b, 30, 0)
	if err != nil {
		t.Fatalf("AllocRuns: %v", err)
	}
	want := []Run{{0, 4}, {5, 15}, {21, 11}}
	if len(runs) != len(want) {
		t.Fatalf("runs = %v, want %v", runs, want)
	}
	for i := range want {
		if runs[i] != want[i] {
			t.Fatalf("runs = %v, want %v", runs, want)
		}
	}
}

func TestAllocRunsMaxRun(t *testing.T) {
	b := NewBitmap(64)
	runs, err := AllocRuns(b, 20, 8)
	if err != nil {
		t.Fatalf("AllocRuns: %v", err)
	}
	var total uint64
	for _, r := range runs {
		if r.Len > 8 {
			t.Errorf("run %v exceeds maxRun 8", r)
		}
		total += r.Len
	}
	if total != 20 {
		t.Errorf("allocated %d blocks, want 20", total)
	}
}

// A request that cannot be satisfied must leave the allocator untouched.
func TestAllocRunsRollsBackOnFailure(t *testing.T) {
	b := NewBitmap(16)
	if _, err := AllocRuns(b, 20, 0); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("AllocRuns beyond capacity: got %v, want ErrNoSpace", err)
	}
	if start, err := b.Alloc(16); err != nil || start != 0 {
		t.Fatalf("after rollback Alloc(16) = (%d, %v), want (0, nil)", start, err)
	}
}

// plainAllocator hides the Bitmap's AllocUpTo (embedding would promote it), so
// AllocRuns has to fall back to probing with Alloc alone.
type plainAllocator struct{ b *Bitmap }

func (p plainAllocator) Alloc(n uint64) (uint64, error) { return p.b.Alloc(n) }
func (p plainAllocator) Free(start, n uint64) error     { return p.b.Free(start, n) }
func (p plainAllocator) Reserve(start, n uint64) error  { return p.b.Reserve(start, n) }

func TestAllocRunsWithoutRunAllocator(t *testing.T) {
	b := NewBitmap(32)
	_ = b.Reserve(16, 1) // [0,16) and [17,32)
	var a Allocator = plainAllocator{b}
	if _, ok := a.(RunAllocator); ok {
		t.Fatal("plainAllocator must not satisfy RunAllocator")
	}
	runs, err := AllocRuns(a, 24, 0)
	if err != nil {
		t.Fatalf("AllocRuns: %v", err)
	}
	var total uint64
	for _, r := range runs {
		total += r.Len
	}
	if total != 24 {
		t.Fatalf("allocated %d blocks, want 24", total)
	}
}

func TestAllocRunsZero(t *testing.T) {
	runs, err := AllocRuns(NewBitmap(8), 0, 0)
	if err != nil || runs != nil {
		t.Fatalf("AllocRuns(0) = (%v, %v), want (nil, nil)", runs, err)
	}
}

// Freed blocks must come back into play so first-fit stays exact even with the
// scan hint.
func TestBitmapHintAfterFree(t *testing.T) {
	b := NewBitmap(64)
	_, _ = b.Alloc(32)
	if err := b.Free(8, 4); err != nil {
		t.Fatalf("Free: %v", err)
	}
	if got, err := b.Alloc(4); err != nil || got != 8 {
		t.Fatalf("Alloc(4) after free = (%d, %v), want (8, nil)", got, err)
	}
}

// The final word of the bitmap is partly out of range: those padding bits must
// never be handed out.
func TestBitmapPartialFinalWord(t *testing.T) {
	b := NewBitmap(70) // 70 bits over two 64-bit words
	runs, err := AllocRuns(b, 70, 0)
	if err != nil {
		t.Fatalf("AllocRuns: %v", err)
	}
	var total uint64
	for _, r := range runs {
		if r.Start+r.Len > 70 {
			t.Fatalf("run %v runs past the managed range", r)
		}
		total += r.Len
	}
	if total != 70 {
		t.Fatalf("allocated %d blocks, want 70", total)
	}
	if _, err := b.Alloc(1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc past the end: got %v, want ErrNoSpace", err)
	}
}
