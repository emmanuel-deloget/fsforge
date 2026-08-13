package ext

import (
	"bytes"
	"encoding/binary"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func TestXattrNameSplitJoin(t *testing.T) {
	cases := []struct {
		name  string
		index uint8
		rest  string
	}{
		{"security.capability", 6, "capability"},
		{"user.comment", 1, "comment"},
		{"trusted.overlay.opaque", 4, "overlay.opaque"},
		{"system.posix_acl_access", 2, ""},
		{"system.posix_acl_default", 3, ""},
		{"system.other", 7, "other"},
		{"nodots", 0, "nodots"},
	}
	for _, tc := range cases {
		idx, rest := splitXattrName(tc.name)
		if idx != tc.index || rest != tc.rest {
			t.Errorf("splitXattrName(%q) = %d,%q; want %d,%q",
				tc.name, idx, rest, tc.index, tc.rest)
		}
		if got := joinXattrName(idx, rest); got != tc.name {
			t.Errorf("joinXattrName(%d,%q) = %q, want %q", idx, rest, got, tc.name)
		}
	}
}

// TestXattrOrderIsStable pins the layout order. It is the kernel's lookup order,
// and it is also what keeps a build reproducible: ranging a map would lay the
// same attributes down differently on every run.
func TestXattrOrderIsStable(t *testing.T) {
	x := map[string][]byte{
		"user.zzz":            []byte("z"),
		"user.a":              []byte("a"),
		"security.capability": []byte("c"),
		"trusted.t":           []byte("t"),
	}
	var first []string
	for i := 0; i < 20; i++ {
		var got []string
		for _, e := range sortedXattrs(x) {
			got = append(got, joinXattrName(e.index, e.name))
		}
		if first == nil {
			first = got
			continue
		}
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("order changed between runs: %v then %v", first, got)
		}
	}
	want := []string{"user.a", "user.zzz", "trusted.t", "security.capability"}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("order = %v, want %v (by index, then name length, then name)", first, want)
	}
}

func TestXattrIbodyRoundTrip(t *testing.T) {
	x := map[string][]byte{
		"security.capability": []byte("0123456789abcdefghij"),
		"user.e":              nil,
	}
	body, ok := encodeXattrIbody(sortedXattrs(x), 96)
	if !ok {
		t.Fatal("attributes should fit in 96 bytes")
	}
	got := decodeXattrIbody(body)
	if len(got) != 2 || !bytes.Equal(got["security.capability"], x["security.capability"]) {
		t.Fatalf("round trip = %v", got)
	}
	if v, ok := got["user.e"]; !ok || len(v) != 0 {
		t.Errorf("empty value lost: %v", got)
	}
}

func TestXattrIbodyReportsOverflow(t *testing.T) {
	x := map[string][]byte{"user.big": []byte(strings.Repeat("x", 200))}
	if _, ok := encodeXattrIbody(sortedXattrs(x), 96); ok {
		t.Error("200 bytes should not fit in 96")
	}
	// Nothing to store is not an overflow.
	if body, ok := encodeXattrIbody(nil, 0); !ok || body != nil {
		t.Error("an empty set should succeed with no area")
	}
}

func TestXattrBlockRoundTrip(t *testing.T) {
	x := map[string][]byte{
		"security.selinux":    []byte("system_u:object_r:etc_t:s0\x00"),
		"security.capability": []byte("0123456789abcdefghij"),
		"user.comment":        []byte(strings.Repeat("x", 120)),
		"trusted.overlay.foo": []byte("bar"),
	}
	block, err := encodeXattrBlock(sortedXattrs(x), 1024)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeXattrBlock(block)
	if len(got) != len(x) {
		t.Fatalf("got %d attributes, want %d: %v", len(got), len(x), got)
	}
	for k, v := range x {
		if !bytes.Equal(got[k], v) {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestXattrBlockRejectsOverflow(t *testing.T) {
	x := map[string][]byte{"user.big": []byte(strings.Repeat("x", 4000))}
	if _, err := encodeXattrBlock(sortedXattrs(x), 1024); err == nil {
		t.Error("4000 bytes should not fit in a 1024-byte block")
	}
}

// TestXattrBlockHashes checks the hashes are filled and depend on content —
// e2fsck recomputes them, so zeroes there are an image it offers to repair.
func TestXattrBlockHashes(t *testing.T) {
	a, err := encodeXattrBlock(sortedXattrs(map[string][]byte{"user.a": []byte("1")}), 1024)
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodeXattrBlock(sortedXattrs(map[string][]byte{"user.a": []byte("2")}), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(a[12:]) == 0 {
		t.Error("block hash left at zero")
	}
	if binary.LittleEndian.Uint32(a[xattrBlockHeaderSize+12:]) == 0 {
		t.Error("entry hash left at zero")
	}
	if binary.LittleEndian.Uint32(a[12:]) == binary.LittleEndian.Uint32(b[12:]) {
		t.Error("block hash does not depend on the value")
	}
}

func TestXattrDecodeRejectsGarbage(t *testing.T) {
	if got := decodeXattrIbody(nil); got != nil {
		t.Error("nil area should decode to nothing")
	}
	if got := decodeXattrIbody(make([]byte, 96)); got != nil {
		t.Error("a zeroed area has no magic and should decode to nothing")
	}
	if got := decodeXattrBlock(make([]byte, 8)); got != nil {
		t.Error("a short block should decode to nothing")
	}
	// A valid header whose entry points outside the buffer must not slice out.
	block, err := encodeXattrBlock(sortedXattrs(map[string][]byte{"user.a": []byte("1")}), 1024)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint16(block[xattrBlockHeaderSize+2:], 60000) // e_value_offs
	if got := decodeXattrBlock(block); len(got) != 0 {
		t.Errorf("an out-of-range value offset should be dropped, got %v", got)
	}
}

// TestXattrsSurviveImageRoundTrip is the end-to-end check over both homes: a
// small set stays inside the inode, a large one moves to a block, and both come
// back through Open.
func TestXattrsSurviveImageRoundTrip(t *testing.T) {
	cases := map[string]map[string][]byte{
		"inline": {"security.capability": []byte("0123456789abcdefghij")},
		"block": {
			"security.selinux":    []byte("system_u:object_r:etc_t:s0\x00"),
			"security.capability": []byte("0123456789abcdefghij"),
			"user.comment":        []byte(strings.Repeat("x", 120)),
		},
	}
	for _, engine := range []struct {
		name string
		make func() *Engine
	}{
		{"ext2", func() *Engine { return NewExt2(testDeps()) }},
		{"ext4", func() *Engine { return NewExt4(testDeps()) }},
	} {
		for name, x := range cases {
			t.Run(engine.name+"/"+name, func(t *testing.T) {
				dev := device.NewMem(16 << 20)
				img, err := engine.make().Format(dev, image.Params{Label: "x"})
				if err != nil {
					t.Fatal(err)
				}
				m := meta(0o644)
				m.Xattrs = x
				if _, err := img.Root().Create("file", tree.Bytes("hi"), m); err != nil {
					t.Fatal(err)
				}
				dm := meta(fs.ModeDir | 0o755)
				dm.Xattrs = x
				if _, err := img.Root().Mkdir("dir", dm); err != nil {
					t.Fatal(err)
				}
				// A fast symlink with attributes: its i_blocks is non-zero while the
				// target still sits in i_block, which used to make the reader treat
				// the target bytes as block numbers.
				sm := meta(fs.ModeSymlink | 0o777)
				sm.Xattrs = x
				if err := img.Root().Symlink("link", "file", sm); err != nil {
					t.Fatal(err)
				}
				if err := img.Finalize(); err != nil {
					t.Fatal(err)
				}

				opened, err := engine.make().Open(dev)
				if err != nil {
					t.Fatal(err)
				}
				root := opened.(interface{ RootNode() *image.Node }).RootNode()
				for _, path := range []string{"file", "dir", "link"} {
					n := childByName(root, path)
					if n == nil {
						t.Fatalf("%s missing", path)
					}
					if len(n.Xattrs) != len(x) {
						t.Errorf("%s: got %d attributes, want %d", path, len(n.Xattrs), len(x))
					}
					for k, v := range x {
						if !bytes.Equal(n.Xattrs[k], v) {
							t.Errorf("%s: %s = %q, want %q", path, k, n.Xattrs[k], v)
						}
					}
				}
				if ln := childByName(root, "link"); ln != nil && ln.Link != "file" {
					t.Errorf("symlink target = %q, want %q", ln.Link, "file")
				}
			})
		}
	}
}
