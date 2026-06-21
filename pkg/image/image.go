// Package image defines the filesystem-agnostic, public API of fsforge: the
// contracts an engine exposes (Image, Dir, File, Filesystem) and the
// dependencies it receives by injection (Deps). Creating a fresh image and
// mutating an existing one offline are the *same* API — Open returns the same
// editable Dir tree as Format, because loading is lazy.
package image

import (
	"github.com/emmanuel-deloget/fsforge/pkg/alloc"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Filesystem is the contract every engine (ext, squashfs, fat, exfat, iso, …)
// implements. The two entry points return the *same* editable Image type, so a
// freshly formatted image and a loaded one are edited identically.
type Filesystem interface {
	// Format lays down a fresh, empty filesystem on dev and returns it open for
	// editing. dev must be at least as large as the format requires; the engine
	// does not resize it.
	Format(dev device.Device, p Params) (Image, error)
	// Open loads an existing filesystem from dev for reading or offline
	// mutation. It returns the same editable Image as Format; loading is lazy,
	// so only metadata is read up front. Engines that are write-only (e.g.
	// squashfs) return an Image whose Finalize reports that it cannot be
	// re-finalized in place.
	Open(dev device.Device) (Image, error)
}

// Params carries engine-agnostic creation options for Format.
type Params struct {
	// Label is the volume label; engines that have no notion of one ignore it.
	Label string
	// BlockSize is the filesystem block size in bytes. Zero selects the
	// engine's default; non-zero values must be valid for the engine (typically
	// a power of two within a format-specific range).
	BlockSize uint32
	// Features carries engine-specific switches keyed by engine-defined names,
	// for options that do not generalise across formats.
	Features map[string]any
}

// Image is an open filesystem, created by Format or Open. Edits are accumulated
// against the in-memory tree through Root and are committed to the device only
// by Finalize, which performs a single deterministic layout pass.
type Image interface {
	// Root returns the editable root directory of the image.
	Root() Dir
	// Finalize lays the accumulated tree out on the device. It is the only
	// method that writes, and it is a pure function of the tree, the allocator
	// and the injected environment — so identical inputs yield identical bytes.
	// After a successful Finalize the image should not be edited further.
	Finalize() error
}

// File is an opaque handle to a regular file within an image, returned by
// Create and used as the target of Link to create a hard link.
type File interface {
	inode() *tree.Inode
}

// Dir is the editable directory interface. The same interface backs both a
// freshly formatted image and a lazily loaded existing one, so create and
// offline-mutate share one editing surface. Names are single path components:
// "", ".", ".." and names containing '/' are rejected.
type Dir interface {
	// Mkdir creates a subdirectory and returns it for further editing. It fails
	// with fs.ErrExist if name is already present.
	Mkdir(name string, m tree.Meta) (Dir, error)
	// Create adds a regular file whose contents are the lazy source c (streamed
	// at Finalize, never buffered) and returns a handle usable as a Link target.
	Create(name string, c tree.Source, m tree.Meta) (File, error)
	// Symlink adds a symbolic link named name pointing at target.
	Symlink(name, target string, m tree.Meta) error
	// Mknod adds a special file (block/char device, fifo or socket); the kind is
	// taken from m.Mode's type bits and rdev carries the device number.
	Mknod(name string, rdev uint64, m tree.Meta) error
	// Link adds name as a hard link to target, an existing regular-file handle
	// returned by Create. The shared inode's link count is incremented.
	Link(name string, target File) error
	// Lookup returns the entry named name, or fs.ErrNotExist if absent.
	Lookup(name string) (*tree.Dirent, error)
	// Remove deletes name. Removing a non-empty directory fails with a
	// "directory not empty" error; a missing name fails with fs.ErrNotExist.
	Remove(name string) error
	// Range calls fn for each entry in insertion order, stopping and returning
	// the first error fn yields.
	Range(fn func(tree.Dirent) error) error
}

// Deps bundles everything an engine receives by injection. Wiring different
// implementations here — and nowhere else — is what selects, for example,
// reproducible behaviour (FixedClock + FixedUUID + Bitmap) versus host
// behaviour. There is deliberately no "reproducible" flag inside engines.
//
// Clock and UUID are normalised to non-nil defaults by NewMem/Adopt; Alloc and
// Log may be nil, in which case engines fall back to their own defaults (the
// deterministic bitmap allocator and a no-op logger).
type Deps struct {
	// Clock is the only source of "now" an engine may use.
	Clock Clock
	// UUID is the only source of filesystem identifiers.
	UUID UUIDSource
	// Alloc builds the block allocator; nil means the engine's default
	// (deterministic bitmap) policy.
	Alloc alloc.Factory
	// Log receives engine diagnostics; nil means no logging.
	Log Logger
}

// Logger is the minimal logging surface engines use. It is satisfied by the
// standard library's *log.Logger, among others.
type Logger interface {
	Printf(format string, args ...any)
}
