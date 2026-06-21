package alloc

import "errors"

// Bitmap is a deterministic first-fit allocator backed by a bitmap: bit i set
// means block i is in use. It always returns the lowest contiguous run that
// fits, so a given sequence of calls yields a byte-identical layout — which is
// what makes images reproducible.
type Bitmap struct {
	bits  []uint64
	total uint64
}

// NewBitmap returns an allocator managing total blocks, all initially free.
func NewBitmap(total uint64) *Bitmap {
	return &Bitmap{bits: make([]uint64, (total+63)/64), total: total}
}

func (b *Bitmap) get(i uint64) bool { return b.bits[i/64]&(1<<(i%64)) != 0 }
func (b *Bitmap) mark(i uint64)     { b.bits[i/64] |= 1 << (i % 64) }
func (b *Bitmap) unmark(i uint64)   { b.bits[i/64] &^= 1 << (i % 64) }

// Alloc reserves the lowest contiguous run of n free blocks and returns its
// start. It fails with ErrNoSpace when no such run exists, and errors on n == 0.
func (b *Bitmap) Alloc(n uint64) (uint64, error) {
	if n == 0 {
		return 0, errors.New("alloc: zero-length allocation")
	}
	var run, start uint64
	for i := uint64(0); i < b.total; i++ {
		if b.get(i) {
			run = 0
			continue
		}
		if run == 0 {
			start = i
		}
		if run++; run == n {
			for j := start; j < start+n; j++ {
				b.mark(j)
			}
			return start, nil
		}
	}
	return 0, ErrNoSpace
}

// Free releases the run of n blocks starting at start. It errors if the run
// extends past the managed range.
func (b *Bitmap) Free(start, n uint64) error {
	if start+n > b.total {
		return errors.New("alloc: free out of range")
	}
	for j := start; j < start+n; j++ {
		b.unmark(j)
	}
	return nil
}

// Reserve marks the run of n blocks starting at start as used, for fixed
// regions such as the superblock or group descriptors. It errors if the run
// extends past the managed range.
func (b *Bitmap) Reserve(start, n uint64) error {
	if start+n > b.total {
		return errors.New("alloc: reserve out of range")
	}
	for j := start; j < start+n; j++ {
		b.mark(j)
	}
	return nil
}

// BitmapFactory builds Bitmap allocators. It is the default injected policy.
type BitmapFactory struct{}

func (BitmapFactory) New(totalBlocks uint64) Allocator { return NewBitmap(totalBlocks) }
