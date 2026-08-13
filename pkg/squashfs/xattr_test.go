package squashfs

import (
	"bytes"
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
		name string
		typ  uint16
		rest string
	}{
		{"user.comment", xattrTypeUser, "comment"},
		{"trusted.overlay.opaque", xattrTypeTrusted, "overlay.opaque"},
		{"security.capability", xattrTypeSecurity, "capability"},
	}
	for _, tc := range cases {
		typ, rest, err := splitXattrName(tc.name)
		if err != nil {
			t.Fatalf("splitXattrName(%q): %v", tc.name, err)
		}
		if typ != tc.typ || rest != tc.rest {
			t.Errorf("splitXattrName(%q) = %d,%q; want %d,%q", tc.name, typ, rest, tc.typ, tc.rest)
		}
		if got := joinXattrName(typ, rest); got != tc.name {
			t.Errorf("joinXattrName(%d,%q) = %q, want %q", typ, rest, got, tc.name)
		}
	}
	// squashfs has no encoding for a name outside the three prefixes, and
	// silently storing it under one of them would be worse than refusing.
	if _, _, err := splitXattrName("system.posix_acl_access"); err == nil {
		t.Error("a name with no encodable prefix should be refused")
	}
}

// TestBasicType covers the mapping that decides what a directory entry says
// about the thing it points at. An extended type here aborts unsquashfs with
// "unknown inode type" even though the inode itself is well formed.
func TestBasicType(t *testing.T) {
	pairs := map[uint16]uint16{
		typeDir: typeDir, typeFile: typeFile, typeSymlink: typeSymlink,
		typeExtDir: typeDir, typeExtFile: typeFile, typeExtSymlink: typeSymlink,
		typeExtBlkdev: typeBlkdev, typeExtChrdev: typeChrdev,
		typeExtFifo: typeFifo, typeExtSocket: typeSocket,
	}
	for in, want := range pairs {
		if got := basicType(in); got != want {
			t.Errorf("basicType(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestXattrListSize pins the id record's size field, which is not the encoded
// length: mksquashfs stores the prefixed name, its terminator and the value.
func TestXattrListSize(t *testing.T) {
	set, err := newXattrSet(map[string][]byte{"user.k": []byte("v")})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := set.listSize(), uint32(len("user.k")+1+len("v")); got != want {
		t.Errorf("listSize = %d, want %d", got, want)
	}
	if got, want := len(set.encode()), 10; got != want {
		t.Errorf("encoded length = %d, want %d (they differ on purpose)", got, want)
	}
}

func TestXattrSetDedup(t *testing.T) {
	a, _ := newXattrSet(map[string][]byte{"user.a": []byte("1"), "user.b": []byte("2")})
	b, _ := newXattrSet(map[string][]byte{"user.b": []byte("2"), "user.a": []byte("1")})
	if a.key != b.key {
		t.Error("the same attributes in a different map order should share a key")
	}
	c, _ := newXattrSet(map[string][]byte{"user.a": []byte("1"), "user.b": []byte("3")})
	if a.key == c.key {
		t.Error("different values must not share a key")
	}
	if !reflect.DeepEqual(a.encode(), b.encode()) {
		t.Error("the same attributes must encode to the same bytes")
	}
}

func TestXattrPairsRoundTrip(t *testing.T) {
	x := map[string][]byte{
		"user.short":   []byte("v"),
		"user.long":    []byte(strings.Repeat("x", 300)),
		"user.empty":   nil,
		"security.cap": []byte("\x00\x01\x02"),
	}
	set, err := newXattrSet(x)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeXattrPairs(set.encode(), len(set.pairs))
	if len(got) != len(x) {
		t.Fatalf("got %d attributes, want %d: %v", len(got), len(x), got)
	}
	for k, v := range x {
		if !bytes.Equal(got[k], v) {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	// Reading fewer pairs than were written stops early rather than running on.
	if n := len(decodeXattrPairs(set.encode(), 1)); n != 1 {
		t.Errorf("count should bound the read, got %d attributes", n)
	}
	if got := decodeXattrPairs(nil, 3); got != nil {
		t.Error("no bytes should decode to nothing")
	}
}

// TestXattrsSurviveImageRoundTrip exercises every extended inode type, since
// each one puts its attribute index at a different offset — the symlink's comes
// after the target rather than before it.
func TestXattrsSurviveImageRoundTrip(t *testing.T) {
	x := map[string][]byte{
		"security.capability": []byte("0123456789abcdefghij"),
		"user.comment":        []byte(strings.Repeat("x", 200)),
	}
	dev := device.NewMem(16 << 20)
	img, err := New(testDeps()).Format(dev, image.Params{})
	if err != nil {
		t.Fatal(err)
	}
	root := img.Root()
	m := meta(0o644)
	m.Xattrs = x
	if _, err := root.Create("file", tree.Bytes(strings.Repeat("p", 5000)), m); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Create("plain", tree.Bytes("p"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	sm := meta(fs.ModeSymlink | 0o777)
	sm.Xattrs = x
	if err := root.Symlink("link", "file", sm); err != nil {
		t.Fatal(err)
	}
	dvm := meta(fs.ModeDevice | fs.ModeCharDevice | 0o600)
	dvm.Xattrs = x
	if err := root.Mknod("chr", 0x0103, dvm); err != nil {
		t.Fatal(err)
	}
	fm := meta(fs.ModeNamedPipe | 0o600)
	fm.Xattrs = x
	if err := root.Mknod("fifo", 0, fm); err != nil {
		t.Fatal(err)
	}
	dm := meta(fs.ModeDir | 0o755)
	dm.Xattrs = x
	sub, err := root.Mkdir("dir", dm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Create("nested", tree.Bytes("n"), m); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatal(err)
	}

	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatal(err)
	}
	rn := opened.(interface{ RootNode() *image.Node }).RootNode()
	for _, path := range []string{"file", "link", "chr", "fifo", "dir"} {
		n := find(rn, path)
		if n == nil {
			t.Fatalf("%s missing", path)
		}
		for k, v := range x {
			if !bytes.Equal(n.Xattrs[k], v) {
				t.Errorf("%s: %s = %q, want %q", path, k, n.Xattrs[k], v)
			}
		}
	}
	if n := find(rn, "plain"); n == nil || len(n.Xattrs) != 0 {
		t.Errorf("a node without attributes should have none")
	}
	if l := find(rn, "link"); l == nil || l.Link != "file" {
		t.Errorf("symlink target lost: %v", l)
	}
	if c := find(rn, "chr"); c == nil || c.Rdev != 0x0103 {
		t.Errorf("device number lost: %v", c)
	}
	if d := find(rn, "dir"); d == nil || find(d, "nested") == nil {
		t.Error("nested child lost")
	}
}

// TestXattrSetsAreShared checks the deduplication reaches the image: two nodes
// with the same attributes must point at one record, not two.
func TestXattrSetsAreShared(t *testing.T) {
	x := map[string][]byte{"user.k": []byte("v")}
	y := map[string][]byte{"user.k": []byte("w")}
	dev := device.NewMem(8 << 20)
	img, err := New(testDeps()).Format(dev, image.Params{})
	if err != nil {
		t.Fatal(err)
	}
	m, m2 := meta(0o644), meta(0o644)
	m.Xattrs, m2.Xattrs = x, y
	for _, name := range []string{"a", "b", "c"} {
		if _, err := img.Root().Create(name, tree.Bytes("x"), m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := img.Root().Create("d", tree.Bytes("x"), m2); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatal(err)
	}

	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatal(err)
	}
	r := opened.(interface{ RootNode() *image.Node })
	_ = r
	// Two distinct sets for four nodes.
	sb, err := parseSuperblock(dev.Bytes()[:superblockSize])
	if err != nil {
		t.Fatal(err)
	}
	idx := make([]byte, xattrIndexSize)
	if _, err := dev.ReadAt(idx, int64(sb.xattrTableStart)); err != nil {
		t.Fatal(err)
	}
	if got := le.Uint32(idx[8:]); got != 2 {
		t.Errorf("image holds %d attribute sets, want 2 for four nodes", got)
	}
}

// TestNoXattrsLeavesImageUnchanged guards the promise that adding this feature
// costs nothing to images that do not use it.
func TestNoXattrsLeavesImageUnchanged(t *testing.T) {
	build := func() []byte {
		dev := device.NewMem(8 << 20)
		img, err := New(testDeps()).Format(dev, image.Params{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := img.Root().Create("f", tree.Bytes("x"), meta(0o644)); err != nil {
			t.Fatal(err)
		}
		if err := img.Finalize(); err != nil {
			t.Fatal(err)
		}
		return dev.Bytes()
	}
	sb, err := parseSuperblock(build()[:superblockSize])
	if err != nil {
		t.Fatal(err)
	}
	if sb.flags&flagNoXattrs == 0 {
		t.Error("an image with no attributes should still set the no-xattrs flag")
	}
	if sb.xattrTableStart != noTable {
		t.Errorf("xattr table start = %#x, want the absent marker", sb.xattrTableStart)
	}
}
