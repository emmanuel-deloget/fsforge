// Package alloc defines the block-allocation policy that engines depend on.
// Allocation is injected rather than hard-coded so that (a) it can be tested in
// isolation and (b) the allocation order — which directly determines the byte
// layout — can be made deterministic, a prerequisite for reproducible images.
package alloc

import "errors"

// Allocator hands out and reclaims runs of fixed-size blocks. Block numbers are
// engine-defined indices, not byte offsets.
type Allocator interface {
	// Alloc reserves a contiguous run of n blocks and returns its start.
	Alloc(n uint64) (start uint64, err error)
	// Free releases a previously allocated run.
	Free(start, n uint64) error
	// Reserve marks a fixed region as used (superblock, group descriptors, …).
	Reserve(start, n uint64) error
}

// Factory builds an Allocator sized for a given total block count. Engines take
// a Factory in their Deps so the concrete policy stays injectable.
type Factory interface {
	New(totalBlocks uint64) Allocator
}

// ErrNoSpace is returned when no contiguous run satisfies a request.
var ErrNoSpace = errors.New("alloc: no contiguous space")
