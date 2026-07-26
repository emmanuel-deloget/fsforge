package alloc

import (
	"errors"
	"math/bits"
)

// Bitmap is a deterministic first-fit allocator backed by a bitmap: bit i set
// means block i is in use. It always returns the lowest contiguous run that
// fits, so a given sequence of calls yields a byte-identical layout — which is
// what makes images reproducible.
//
// Scanning works a 64-bit word at a time from a "nothing below this is free"
// hint, so laying out an image with many objects stays close to linear in the
// number of blocks rather than quadratic.
type Bitmap struct {
	bits  []uint64
	total uint64
	hint  uint64 // no block below hint is free
}

var errZeroLen = errors.New("alloc: zero-length allocation")

// NewBitmap returns an allocator managing total blocks, all initially free.
func NewBitmap(total uint64) *Bitmap {
	b := &Bitmap{bits: make([]uint64, (total+63)/64), total: total}
	// Mark the padding bits of the final word as used, so word-at-a-time scans
	// never report a free block past the managed range.
	for i := total; i < uint64(len(b.bits))*64; i++ {
		b.mark(i)
	}
	return b
}

func (b *Bitmap) mark(i uint64)   { b.bits[i/64] |= 1 << (i % 64) }
func (b *Bitmap) unmark(i uint64) { b.bits[i/64] &^= 1 << (i % 64) }

// firstFree returns the lowest free block at or after from, or total if there
// is none.
func (b *Bitmap) firstFree(from uint64) uint64 {
	if from >= b.total {
		return b.total
	}
	w := b.bits[from/64] | (1<<(from%64) - 1) // pretend bits before from are used
	for i := from / 64; ; {
		if w != ^uint64(0) {
			return i*64 + uint64(bits.TrailingZeros64(^w))
		}
		if i++; i >= uint64(len(b.bits)) {
			return b.total
		}
		w = b.bits[i]
	}
}

// firstUsed returns the lowest used block at or after from — the end of the free
// run starting at from — or total if the range is free to the end.
func (b *Bitmap) firstUsed(from uint64) uint64 {
	if from >= b.total {
		return b.total
	}
	w := b.bits[from/64] &^ (1<<(from%64) - 1) // ignore bits before from
	for i := from / 64; ; {
		if w != 0 {
			return min(i*64+uint64(bits.TrailingZeros64(w)), b.total)
		}
		if i++; i >= uint64(len(b.bits)) {
			return b.total
		}
		w = b.bits[i]
	}
}

// take marks the run [start, start+n) as used and refreshes the hint.
func (b *Bitmap) take(start, n uint64) {
	for j := start; j < start+n; j++ {
		b.mark(j)
	}
	b.hint = b.firstFree(b.hint)
}

// Alloc reserves the lowest contiguous run of n free blocks and returns its
// start. It fails with ErrNoSpace when no such run exists, and errors on n == 0.
func (b *Bitmap) Alloc(n uint64) (uint64, error) {
	if n == 0 {
		return 0, errZeroLen
	}
	for i := b.firstFree(b.hint); i < b.total; {
		end := b.firstUsed(i)
		if end-i >= n {
			b.take(i, n)
			return i, nil
		}
		i = b.firstFree(end)
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
	if start < b.hint {
		b.hint = start // keep first-fit exact: freed blocks come back into play
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
	b.take(start, n)
	return nil
}

// BitmapFactory builds Bitmap allocators. It is the default injected policy.
type BitmapFactory struct{}

func (BitmapFactory) New(totalBlocks uint64) Allocator { return NewBitmap(totalBlocks) }
