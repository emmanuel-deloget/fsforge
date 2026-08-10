// Package fsgen builds pseudo-random filesystem trees for differential tests.
//
// The point is not volume but shape: the trees deliberately contain the cases
// that break writers — names at the length limit, names that are not valid
// UTF-8, hard links, empty and multi-block files, device nodes, deep nesting,
// setuid bits, non-zero owners. A test that only ever writes "etc/hosts" proves
// very little, and every one of these has a different failure mode on disk.
//
// Generation is deterministic: the same seed yields the same tree, so a failure
// is reproducible from the seed printed in the failure message. Trees are built
// through the public editing API rather than by appending to Children, so they
// are exactly what a caller could have built.
package fsgen

import (
	"fmt"
	"io/fs"
	"math/rand"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Caps describes what a target format can hold. The generator emits only what
// the format can carry, so a diff reports real losses rather than things the
// format never claimed to support.
type Caps struct {
	MaxName   int  // longest entry name in bytes; 0 means 255
	MaxLink   int  // longest symlink target; 0 means 200
	MaxDepth  int  // deepest directory nesting; 0 means 8
	Symlinks  bool // symbolic links
	Devices   bool // block/char devices and fifos
	HardLinks bool // several names sharing one inode
	NonUTF8   bool // names that are legal bytes but not valid UTF-8
	Owners    bool // non-zero uid/gid
	MaxUID    uint32
	MaxGID    uint32 // cramfs stores gid in eight bits; 0 means no cap
	SpecMode  bool   // setuid/setgid/sticky bits
	Times     bool   // per-node mtimes rather than one fixed time
}

// Options controls one generation.
type Options struct {
	Seed  int64
	Files int // approximate number of leaf nodes; 0 means 40
	Caps  Caps
}

// Generate builds a tree under a fresh in-memory image and returns its root.
// The image is returned too, because the caller usually wants to graft it.
func Generate(deps image.Deps, o Options) (*image.Mem, error) {
	if o.Files == 0 {
		o.Files = 40
	}
	if o.Caps.MaxName == 0 {
		o.Caps.MaxName = 255
	}
	if o.Caps.MaxLink == 0 {
		o.Caps.MaxLink = 200
	}
	if o.Caps.MaxDepth == 0 {
		o.Caps.MaxDepth = 8
	}
	mem := image.NewMem(deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	g := &gen{rnd: rand.New(rand.NewSource(o.Seed)), o: o, names: map[string]bool{}}
	if err := g.fill(mem.Root(), 0); err != nil {
		return nil, err
	}
	if err := g.hardLinks(mem.Root()); err != nil {
		return nil, err
	}
	return mem, nil
}

type gen struct {
	rnd     *rand.Rand
	o       Options
	made    int
	names   map[string]bool // uniqueness across one directory level
	firstFH image.File      // a file handle to hard-link against
}

// fill populates dir, recursing while the budget and the depth limit allow.
// The root keeps going until the budget is spent, so the file count is the
// caller's to choose; deeper levels take a slice of what is left.
func (g *gen) fill(dir image.Dir, depth int) error {
	for i := 0; g.made < g.o.Files; i++ {
		if depth > 0 && i >= 2+g.rnd.Intn(4) {
			return nil
		}
		switch {
		case depth < g.o.Caps.MaxDepth && g.rnd.Intn(3) == 0:
			name, err := g.uniqueName(depth)
			if err != nil {
				return err
			}
			sub, err := dir.Mkdir(name, g.meta(fs.ModeDir|g.perm(0o755)))
			if err != nil {
				return err
			}
			if err := g.fill(sub, depth+1); err != nil {
				return err
			}
		default:
			if err := g.leaf(dir, depth); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *gen) leaf(dir image.Dir, depth int) error {
	name, err := g.uniqueName(depth)
	if err != nil {
		return err
	}
	g.made++

	switch pick := g.rnd.Intn(10); {
	case pick == 0 && g.o.Caps.Symlinks:
		// A long target is its own case: Rock Ridge stores it inline in the
		// directory record, so it competes with the name for the same 255 bytes.
		target := []string{"../elsewhere", "/absolute/target", "sibling",
			longName(g.o.Caps.MaxLink, 'l')}[g.rnd.Intn(4)]
		return dir.Symlink(name, target, g.meta(fs.ModeSymlink|0o777))
	case pick == 1 && g.o.Caps.Devices:
		kinds := []fs.FileMode{fs.ModeDevice | fs.ModeCharDevice, fs.ModeDevice, fs.ModeNamedPipe}
		k := kinds[g.rnd.Intn(len(kinds))]
		rdev := uint64(g.rnd.Intn(256))<<8 | uint64(g.rnd.Intn(256))
		if k&fs.ModeNamedPipe != 0 {
			rdev = 0
		}
		return dir.Mknod(name, rdev, g.meta(k|g.perm(0o644)))
	default:
		h, err := dir.Create(name, g.content(), g.meta(g.perm(0o644)))
		if err != nil {
			return err
		}
		if g.firstFH == nil {
			g.firstFH = h
		}
		return nil
	}
}

// content returns file bodies spanning the sizes that change on-disk layout:
// empty, sub-block, exactly one block, several blocks, and a run of zeroes that
// a sparse-aware writer may or may not punch out.
func (g *gen) content() tree.Source {
	switch g.rnd.Intn(6) {
	case 0:
		return tree.Bytes(nil)
	case 1:
		return tree.Bytes(g.bytes(1 + g.rnd.Intn(200)))
	case 2:
		return tree.Bytes(g.bytes(4096))
	case 3:
		return tree.Bytes(g.bytes(4096*3 + 17))
	case 4:
		return tree.Bytes(make([]byte, 8192)) // all zeroes
	default:
		return tree.Bytes(g.bytes(1 + g.rnd.Intn(9000)))
	}
}

func (g *gen) bytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(g.rnd.Intn(256))
	}
	return b
}

// hardLinks adds a second and third name for one file, so the readback has to
// recover the sharing rather than duplicate the contents.
func (g *gen) hardLinks(root image.Dir) error {
	if !g.o.Caps.HardLinks || g.firstFH == nil {
		return nil
	}
	for i := 0; i < 2; i++ {
		if err := root.Link(fmt.Sprintf("hardlink-%d", i), g.firstFH); err != nil {
			return err
		}
	}
	return nil
}

func (g *gen) meta(mode fs.FileMode) tree.Meta {
	m := tree.Meta{Mode: mode, ModTime: time.Unix(1_600_000_000, 0).UTC()}
	if g.o.Caps.Times {
		m.ModTime = time.Unix(1_600_000_000+int64(g.rnd.Intn(50_000_000)), 0).UTC()
	}
	if g.o.Caps.Owners {
		m.UID = cap32(uint32(g.rnd.Intn(3)*1000), g.o.Caps.MaxUID)
		m.GID = cap32(uint32(g.rnd.Intn(3)*1000), g.o.Caps.MaxGID)
	}
	return m
}

// cap32 keeps an id inside what the format's field can hold, so a diff reports
// a dropped owner rather than the arithmetic of a too-narrow field.
func cap32(v, max uint32) uint32 {
	if max == 0 || v <= max {
		return v
	}
	return v % (max + 1)
}

// perm adds the odd setuid/setgid/sticky bit, which several formats store in a
// different word from the permission bits and therefore drop independently.
func (g *gen) perm(base fs.FileMode) fs.FileMode {
	if !g.o.Caps.SpecMode || g.rnd.Intn(5) != 0 {
		return base
	}
	return base | []fs.FileMode{fs.ModeSetuid, fs.ModeSetgid, fs.ModeSticky}[g.rnd.Intn(3)]
}
