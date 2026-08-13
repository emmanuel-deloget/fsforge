package erofs

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
		name  string
		index uint8
		rest  string
	}{
		{"security.capability", 6, "capability"},
		{"user.comment", 1, "comment"},
		{"trusted.overlay.opaque", 4, "overlay.opaque"},
		{"system.posix_acl_access", 2, ""},
		{"lustre.x", 5, "x"},
		{"nodots", 0, "nodots"},
	}
	for _, tc := range cases {
		idx, rest := splitXattrName(tc.name)
		if idx != tc.index || rest != tc.rest {
			t.Errorf("splitXattrName(%q) = %d,%q; want %d,%q", tc.name, idx, rest, tc.index, tc.rest)
		}
		if got := joinXattrName(idx, rest); got != tc.name {
			t.Errorf("joinXattrName(%d,%q) = %q, want %q", idx, rest, got, tc.name)
		}
	}
}

// TestXattrICountRoundTrip pins the size accounting. i_xattr_icount is what
// tells a reader where the *next* inode begins, so an off-by-one here does not
// corrupt an attribute — it desynchronises the whole inode area.
func TestXattrICountRoundTrip(t *testing.T) {
	for _, x := range []map[string][]byte{
		{"user.a": []byte("1")},
		{"security.capability": []byte("0123456789abcdefghij")},
		{"user.a": []byte("1"), "user.bb": []byte("22"), "trusted.c": []byte("333")},
		{"user.empty": nil},
	} {
		entries := sortedXattrs(x)
		size := xattrInlineSize(entries)
		if size%xattrAlign != 0 {
			t.Errorf("area size %d is not %d-aligned", size, xattrAlign)
		}
		if got := int(xattrIbodySize(xattrICount(size))); got != size {
			t.Errorf("icount round trip: size %d -> icount %d -> %d",
				size, xattrICount(size), got)
		}
	}
	if xattrICount(0) != 0 {
		t.Error("no attributes must mean icount 0")
	}
	if xattrIbodySize(0) != 0 {
		t.Error("icount 0 must mean no area")
	}
}

func TestXattrEncodeDecode(t *testing.T) {
	x := map[string][]byte{
		"security.selinux":    []byte("system_u:object_r:etc_t:s0\x00"),
		"security.capability": []byte("0123456789abcdefghij"),
		"user.comment":        []byte(strings.Repeat("x", 200)),
		"user.empty":          nil,
	}
	area, err := encodeXattrs(sortedXattrs(x))
	if err != nil {
		t.Fatal(err)
	}
	got := decodeXattrs(area)
	if len(got) != len(x) {
		t.Fatalf("got %d attributes, want %d: %v", len(got), len(x), got)
	}
	for k, v := range x {
		if !bytes.Equal(got[k], v) {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestXattrOrderIsStable(t *testing.T) {
	x := map[string][]byte{
		"user.zzz": []byte("z"), "user.a": []byte("a"),
		"security.capability": []byte("c"), "trusted.t": []byte("t"),
	}
	var first []byte
	for i := 0; i < 20; i++ {
		area, err := encodeXattrs(sortedXattrs(x))
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = area
			continue
		}
		if !reflect.DeepEqual(first, area) {
			t.Fatal("the same attributes encoded to different bytes")
		}
	}
}

func TestXattrRejectsOversized(t *testing.T) {
	long := strings.Repeat("n", 300)
	if _, err := encodeXattrs([]xattrEntry{{index: 1, name: long}}); err == nil {
		t.Error("a name over 255 bytes should be refused")
	}
	if _, err := encodeXattrs([]xattrEntry{{index: 1, name: "a", value: make([]byte, 70000)}}); err == nil {
		t.Error("a value over 65535 bytes should be refused")
	}
}

func TestXattrDecodeRejectsGarbage(t *testing.T) {
	if decodeXattrs(nil) != nil {
		t.Error("nil area should decode to nothing")
	}
	if decodeXattrs(make([]byte, xattrHeaderSize)) != nil {
		t.Error("a header with no entries should decode to nothing")
	}
	// An entry claiming more than the area holds must be dropped, not sliced.
	area, err := encodeXattrs(sortedXattrs(map[string][]byte{"user.a": []byte("1")}))
	if err != nil {
		t.Fatal(err)
	}
	le.PutUint16(area[xattrHeaderSize+2:], 60000) // e_value_size
	if got := decodeXattrs(area); len(got) != 0 {
		t.Errorf("an over-long value should be dropped, got %v", got)
	}
}

// TestXattrsSurviveImageRoundTrip is the layout check: several inodes each
// carrying attributes, so a miscounted area shows up as a neighbour read from
// the middle of somebody else's attributes.
func TestXattrsSurviveImageRoundTrip(t *testing.T) {
	x := map[string][]byte{
		"security.capability": []byte("0123456789abcdefghij"),
		"user.comment":        []byte(strings.Repeat("x", 100)),
	}
	dev := device.NewMem(16 << 20)
	img, err := New(testDeps()).Format(dev, image.Params{Label: "x"})
	if err != nil {
		t.Fatal(err)
	}
	root := img.Root()
	m := meta(0o644)
	m.Xattrs = x
	for _, name := range []string{"a", "b", "c", "d"} {
		if _, err := root.Create(name, tree.Bytes("payload"), m); err != nil {
			t.Fatal(err)
		}
	}
	// A node without attributes between two that have them: its neighbours must
	// still be found where the layout says they are.
	if _, err := root.Create("plain", tree.Bytes("p"), meta(0o644)); err != nil {
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
	for _, path := range []string{"a", "b", "c", "d", "dir"} {
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
		t.Errorf("a node without attributes should have none: %v", n)
	}
	if d := find(rn, "dir"); d == nil || find(d, "nested") == nil {
		t.Error("nested child lost")
	}
}
