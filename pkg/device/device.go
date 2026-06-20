// Package device defines the block-level abstraction that every filesystem
// engine reads from and writes to. It is the lowest layer of fsforge and
// depends on nothing else in the module, which keeps the dependency graph
// acyclic and lets engines be tested against an in-memory device.
package device

import "io"

// Device is a fixed-size, randomly addressable block of storage. Engines only
// ever see this interface, never a concrete *os.File, so any backend (file,
// memory, a slice of a larger device) can be injected.
type Device interface {
	io.ReaderAt
	io.WriterAt
	// Size reports the total addressable size in bytes.
	Size() int64
}

// Discarder is an optional capability: a Device that can punch holes so that
// unwritten regions stay sparse on the host. Engines detect it with a type
// assertion and degrade gracefully when it is absent.
type Discarder interface {
	// Discard releases the given byte range; subsequent reads return zeros.
	Discard(off, length int64) error
}
