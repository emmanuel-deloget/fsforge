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

// RunAllocator is an optional capability: an Allocator that can hand out the
// largest run it currently has instead of failing when the exact request does
// not fit. AllocRuns detects it with a type assertion and falls back to probing
// with Alloc when it is absent, so the mandatory contract stays minimal.
type RunAllocator interface {
	Allocator
	// AllocUpTo reserves at most n blocks from the run it would otherwise pick
	// for Alloc and reports how many it got. It returns ErrNoSpace only when no
	// block at all is free.
	AllocUpTo(n uint64) (start, got uint64, err error)
}

// Factory builds an Allocator sized for a given total block count. Engines take
// a Factory in their Deps so the concrete policy stays injectable.
type Factory interface {
	New(totalBlocks uint64) Allocator
}

// ErrNoSpace is returned when no contiguous run satisfies a request.
var ErrNoSpace = errors.New("alloc: no contiguous space")

// Run is a contiguous span of blocks.
type Run struct {
	Start uint64
	Len   uint64
}

// AllocRuns reserves n blocks as one or more runs, no run longer than maxRun
// (0 means unlimited). It exists because filesystem objects rarely need one
// unbroken span: an ext4 extent caps at 32768 blocks and per-group metadata
// caps any free run at one block group, so a large file must be spread over
// several runs or it cannot be laid out at all, however much space is free.
//
// Each run is as long as the allocator can make it: the full remainder when it
// fits contiguously, otherwise the largest run available. On failure every run
// taken here is released, so a failed call reserves nothing.
func AllocRuns(a Allocator, n, maxRun uint64) ([]Run, error) {
	if n == 0 {
		return nil, nil
	}
	var runs []Run
	for left := n; left > 0; {
		want := left
		if maxRun > 0 && want > maxRun {
			want = maxRun
		}
		start, got, err := allocAtMost(a, want)
		if err != nil {
			for _, r := range runs {
				_ = a.Free(r.Start, r.Len)
			}
			return nil, err
		}
		runs = append(runs, Run{Start: start, Len: got})
		left -= got
	}
	return runs, nil
}

// allocAtMost reserves as much of want as one run can hold, preferring a run
// that covers want entirely.
func allocAtMost(a Allocator, want uint64) (start, got uint64, err error) {
	if start, err = a.Alloc(want); err == nil {
		return start, want, nil
	}
	if !errors.Is(err, ErrNoSpace) {
		return 0, 0, err
	}
	if ra, ok := a.(RunAllocator); ok {
		return ra.AllocUpTo(want)
	}
	// Plain Allocator: halve the request until a run fits. Deterministic, since
	// it only depends on the sequence of requests.
	for want /= 2; want > 0; want /= 2 {
		if start, err = a.Alloc(want); err == nil {
			return start, want, nil
		}
		if !errors.Is(err, ErrNoSpace) {
			return 0, 0, err
		}
	}
	return 0, 0, ErrNoSpace
}
