package fsforge_test

import (
	"flag"
	"strings"
	"testing"
	"time"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/internal/fsgen"
	"github.com/emmanuel-deloget/fsforge/internal/manifest"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// seed lets a failure be replayed: the message prints the seed that produced
// the tree, and -fsgen.seed feeds it back in.
var seed = flag.Int64("fsgen.seed", 20260810, "seed for the generated trees")

// target describes one engine: what its format can hold, and therefore what a
// round trip is expected to preserve.
//
// The two are stated separately on purpose. Caps stops the generator emitting
// what the format never claimed to support; Keeps states what must survive of
// what *was* emitted. A field missing from Keeps is a documented, deliberate
// loss — every one of them below was established by running this test, not by
// reading a specification and hoping.
type target struct {
	fs     string
	caps   fsgen.Caps
	keeps  manifest.Field
	why    string        // why keeps is not All
	minted []string      // paths the format creates on its own
	grain  time.Duration // coarsest timestamp resolution; 0 means one second
}

var targets = []target{{
	fs: "ext2",
	caps: fsgen.Caps{Symlinks: true, Devices: true, HardLinks: true,
		NonUTF8: true, Owners: true, SpecMode: true, Times: true, Xattrs: true},
	keeps:  manifest.All,
	minted: []string{"lost+found"},
}, {
	fs: "ext4",
	caps: fsgen.Caps{Symlinks: true, Devices: true, HardLinks: true,
		NonUTF8: true, Owners: true, SpecMode: true, Times: true, Xattrs: true},
	keeps:  manifest.All,
	minted: []string{"lost+found"},
}, {
	fs: "squashfs",
	caps: fsgen.Caps{Symlinks: true, Devices: true, HardLinks: true,
		NonUTF8: true, Owners: true, SpecMode: true, Times: true},
	keeps: manifest.All &^ manifest.Xattrs &^ manifest.Nlink,
	why: "the superblock sets the no-xattrs flag; and a basic-file inode carries " +
		"no nlink field, so a hard-linked regular file reads back with a count of " +
		"1 — the sharing itself survives through the inode reference, which is " +
		"what manifest.Links checks",
}, {
	fs: "erofs",
	caps: fsgen.Caps{Symlinks: true, Devices: true, HardLinks: true,
		NonUTF8: true, Owners: true, SpecMode: true, Times: true, Xattrs: true},
	keeps: manifest.All,
}, {
	fs: "cpio",
	caps: fsgen.Caps{Symlinks: true, Devices: true, HardLinks: true,
		NonUTF8: true, Owners: true, SpecMode: true, Times: true},
	keeps: manifest.All &^ manifest.Xattrs,
	why:   "newc has no xattr records",
}, {
	// An ISO directory record is capped at 255 bytes and carries its Rock Ridge
	// entries — the long name and the symlink target both — inline, so the two
	// compete for one budget. The engine says so rather than truncating; CE
	// continuation records, which is how a real ISO escapes the cap, are not
	// implemented. Depth is capped at five because ISO9660 allows eight levels
	// and the engine does not relocate deeper trees.
	// Names are encoded, not copied, so bytes that are not valid UTF-8 cannot
	// round-trip; the engine refuses them rather than mangling them.
	fs: "iso",
	caps: fsgen.Caps{MaxName: 64, MaxLink: 48, MaxDepth: 5, Symlinks: true,
		Devices: true, Owners: true, SpecMode: true, Times: true},
	keeps: manifest.All &^ manifest.Xattrs &^ manifest.Nlink,
	why: "Rock Ridge carries everything else, but the reader does not fold " +
		"records sharing an extent back into one node, so hard links come back " +
		"as independent copies",
}, {
	// namelen is six four-byte units wide, so 252 bytes is the ceiling; longer
	// names are refused by the writer rather than wrapped to an empty name.
	//
	// HardLinks is off because cramfs has no notion of one — every entry carries
	// its own inline inode — and the writer currently emits an image whose
	// shared node reads back as corrupt data rather than either duplicating the
	// contents or refusing. Turning this on is how to reproduce it.
	fs: "cramfs",
	caps: fsgen.Caps{MaxName: 252, Symlinks: true, Devices: true, MaxGID: 255,
		NonUTF8: true, Owners: true, SpecMode: true},
	keeps: manifest.All &^ manifest.Xattrs &^ manifest.Nlink &^ manifest.MTime,
	why: "cramfs stores no timestamps, no link count and no xattrs; its gid " +
		"field is eight bits wide, which MaxGID keeps the generator inside",
}, {
	// UDF names are Unicode, not bytes: CS0 encodes them, so a name that is not
	// valid UTF-8 cannot survive by construction. The length cap is conservative
	// — a name is at most 255 bytes on disk and CS0 spends two bytes per BMP
	// character, so 120 characters is safe whatever the script.
	fs: "udf",
	caps: fsgen.Caps{MaxName: 120, Symlinks: true, Devices: true, HardLinks: true,
		Owners: true, SpecMode: true, Times: true},
	keeps: manifest.All &^ manifest.Xattrs,
	why:   "UDF extended attributes are not written",
}, {
	// romfs is a minimal read-only format: no timestamps, no owner, and no
	// setuid/setgid/sticky bits — it stores little more than the rwx triplet.
	//
	// HardLinks is off because the writer mislays them, not because the format
	// cannot express them (it has a hard-link node type). The layout keys header
	// offsets by *image.Node, so several names sharing one node overwrite each
	// other's offset and the sibling chain then points into the wrong subtree —
	// whole directories move. Turning this on is how to reproduce it.
	fs:   "romfs",
	caps: fsgen.Caps{Symlinks: true, Devices: true, NonUTF8: true},
	keeps: manifest.All &^ manifest.Xattrs &^ manifest.MTime &^ manifest.Owner &^
		manifest.Nlink,
	why: "romfs stores no timestamps and no owner",
}, {
	// exFAT is a DOS-lineage format: no owner, no symlinks, no devices, no hard
	// links, no setuid bits, names are UTF-16. Timestamps have two-second
	// resolution, which grain covers rather than hiding.
	fs:    "exfat",
	caps:  fsgen.Caps{MaxName: 200, Times: true},
	keeps: manifest.All &^ manifest.Xattrs &^ manifest.Owner,
	why:   "exFAT has no owner field",
	grain: 2 * time.Second,
}}

func TestDifferentialRoundTrip(t *testing.T) {
	for _, tg := range targets {
		t.Run(tg.fs, func(t *testing.T) {
			src, err := fsgen.Generate(fsforge.ReproducibleDeps(1600000000), fsgen.Options{
				Seed: *seed,
				Caps: tg.caps,
			})
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			want, err := manifest.FromTree(src.RootNode())
			if err != nil {
				t.Fatalf("manifest of source: %v", err)
			}

			got := roundTrip(t, tg.fs, src.RootNode())
			diffs := manifest.Diff(want, got, manifest.Options{
				Fields: tg.keeps, MTimeGrain: tg.grain, Minted: tg.minted,
			})
			if len(diffs) > 0 {
				t.Errorf("%s round trip lost metadata over %d entries (seed %d, checked %s)\n%s",
					tg.fs, len(want), *seed, tg.keeps, strings.Join(diffs, "\n"))
			}
		})
	}
}

// roundTrip writes root with the named engine, reopens the bytes, and returns
// the manifest of what came back.
func roundTrip(t *testing.T, fstype string, root *image.Node) manifest.Manifest {
	t.Helper()
	deps := fsforge.ReproducibleDeps(1600000000)
	eng, err := fsforge.EngineFor(fstype, deps, 0)
	if err != nil {
		t.Fatalf("EngineFor: %v", err)
	}
	dev := device.NewMem(64 << 20)
	img, err := eng.Format(dev, image.Params{Label: "diff"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if err := fsforge.Graft(img.Root(), root); err != nil {
		t.Fatalf("Graft: %v", err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	opened, err := eng.Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rn, ok := opened.(interface{ RootNode() *image.Node })
	if !ok {
		t.Fatalf("%s: opened image exposes no RootNode", fstype)
	}
	m, err := manifest.FromTree(rn.RootNode())
	if err != nil {
		t.Fatalf("manifest of readback: %v", err)
	}
	return m
}
