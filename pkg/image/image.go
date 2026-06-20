// Package image defines the filesystem-agnostic, public API of fsforge: the
// contracts an engine exposes (Image, Dir, File, Filesystem) and the
// dependencies it receives by injection (Deps). Creating a fresh image and
// mutating an existing one offline are the *same* API — Open returns the same
// editable Dir tree as Format, because loading is lazy.
package image

import (
	"github.com/edeloget/fsforge/pkg/alloc"
	"github.com/edeloget/fsforge/pkg/device"
	"github.com/edeloget/fsforge/pkg/tree"
)

// Filesystem is the contract every engine (ext, squashfs, …) implements.
type Filesystem interface {
	// Format lays down a fresh, empty filesystem on dev.
	Format(dev device.Device, p Params) (Image, error)
	// Open loads an existing filesystem for reading or offline mutation.
	Open(dev device.Device) (Image, error)
}

// Params carries engine-agnostic creation options. Engine-specific switches are
// passed through Features, keyed by engine-defined names.
type Params struct {
	Label     string
	BlockSize uint32
	Features  map[string]any
}

// Image is an open filesystem. Mutations are accumulated against the tree and
// committed to the device only by Finalize, which performs a deterministic
// layout pass.
type Image interface {
	Root() Dir
	Finalize() error
}

// File is an opaque handle to a regular file, used as the target of Link.
type File interface {
	inode() *tree.Inode
}

// Dir is the editable directory interface. The same interface backs both a
// freshly formatted image and a lazily loaded existing one.
type Dir interface {
	Mkdir(name string, m tree.Meta) (Dir, error)
	Create(name string, c tree.Source, m tree.Meta) (File, error)
	Symlink(name, target string, m tree.Meta) error
	Mknod(name string, rdev uint64, m tree.Meta) error
	Link(name string, target File) error
	Lookup(name string) (*tree.Dirent, error)
	Remove(name string) error
	Range(fn func(tree.Dirent) error) error
}

// Deps bundles everything an engine receives by injection. Wiring different
// implementations here — and nowhere else — is what selects, for example,
// reproducible behaviour (FixedClock + FixedUUID + Bitmap) versus host
// behaviour. There is deliberately no "reproducible" flag inside engines.
type Deps struct {
	Clock Clock
	UUID  UUIDSource
	Alloc alloc.Factory
	Log   Logger
}

// Logger is the minimal logging surface engines use; nil-safe wrappers are the
// caller's responsibility via a no-op implementation.
type Logger interface {
	Printf(format string, args ...any)
}
