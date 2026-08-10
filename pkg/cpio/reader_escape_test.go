package cpio

import (
	"errors"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// appendEntry renders one newc entry (header, NUL-terminated name, padded body)
// the way the reader expects to find it.
func appendEntry(buf []byte, name string, mode uint32, body string) []byte {
	h := hdr{
		mode:     mode,
		nlink:    1,
		filesize: uint32(len(body)),
		namesize: uint32(len(name) + 1),
	}
	buf = append(buf, h.marshal()...)
	buf = append(buf, name...)
	buf = append(buf, 0)
	for len(buf)%4 != 0 {
		buf = append(buf, 0)
	}
	buf = append(buf, body...)
	for len(buf)%4 != 0 {
		buf = append(buf, 0)
	}
	return buf
}

func archive(t *testing.T, name string, mode uint32, body string) device.Device {
	t.Helper()
	buf := appendEntry(nil, name, mode, body)
	buf = appendEntry(buf, trailerName, 0, "")
	dev := device.NewMem(int64(len(buf)) + 4096)
	if _, err := dev.WriteAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	return dev
}

// TestOpenRejectsEscapingNames covers the cpio-specific hole: splitPath drops
// "." and empty components but not "..", so an archive naming its entries
// relative to somewhere above the root used to sink directories called ".."
// straight into the tree.
func TestOpenRejectsEscapingNames(t *testing.T) {
	for _, name := range []string{
		"../../etc/passwd",
		"../escape",
		"etc/../../escape",
		"..",
	} {
		t.Run(name, func(t *testing.T) {
			e := New(testDeps())
			_, err := e.Open(archive(t, name, 0o100644, "owned"))
			if !errors.Is(err, image.ErrBadName) {
				t.Fatalf("Open(%q) = %v, want ErrBadName", name, err)
			}
		})
	}
}

// TestOpenAcceptsOrdinaryNames guards the other direction: "./" prefixes and
// leading dots are ordinary in real archives.
func TestOpenAcceptsOrdinaryNames(t *testing.T) {
	e := New(testDeps())
	img, err := e.Open(archive(t, "./etc/..keep", 0o100644, "k"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	root := img.(*cImageRead).RootNode()
	etc := find(root, "etc")
	if etc == nil || find(etc, "..keep") == nil {
		t.Fatal("ordinary name was rejected")
	}
}
