package udf

import (
	"bytes"
	"io/fs"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func find(n *image.Node, name string) *image.Node {
	for _, e := range n.Children {
		if e.Name == name {
			return e.Node
		}
	}
	return nil
}

func readAll(t *testing.T, s tree.Source) []byte {
	t.Helper()
	if s == nil {
		t.Fatal("nil source")
	}
	b := make([]byte, s.Size())
	s.ReadAt(b, 0)
	return b
}

func TestOpenRoundTrip(t *testing.T) {
	dev := device.NewMem(8 << 20)
	buildSample(t, dev)

	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	root := opened.(interface{ RootNode() *image.Node }).RootNode()

	if etc := find(root, "etc"); etc == nil || string(readAll(t, find(etc, "hosts").Content)) != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts mismatch")
	}
	if got := readAll(t, find(root, "data.bin").Content); !bytes.Equal(got, sampleBytes(5000)) {
		t.Errorf("data.bin mismatch: %d bytes", len(got))
	}
	if e := find(root, "empty"); e == nil || e.Content == nil || e.Content.Size() != 0 {
		t.Errorf("empty file lost: %+v", e)
	}
	if ln := find(root, "link"); ln == nil || ln.Mode&fs.ModeSymlink == 0 || ln.Link != "etc/hosts" {
		t.Errorf("symlink lost: %+v", ln)
	}
	if dn := find(root, "null"); dn == nil || dn.Mode&fs.ModeCharDevice == 0 || dn.Rdev != 0x0103 {
		t.Errorf("device node lost: %+v", dn)
	}
	if d := find(find(find(root, "a"), "b"), "deep.txt"); d == nil || string(readAll(t, d.Content)) != "deep\n" {
		t.Errorf("nested file lost")
	}
}

func TestOpenModes(t *testing.T) {
	dev := device.NewMem(8 << 20)
	buildSample(t, dev)
	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatal(err)
	}
	root := opened.(interface{ RootNode() *image.Node }).RootNode()

	if m := find(root, "etc").Mode; m&fs.ModeDir == 0 || m.Perm() != 0o755 {
		t.Errorf("etc mode = %v", m)
	}
	if m := find(find(root, "etc"), "hosts").Mode; m.Perm() != 0o644 {
		t.Errorf("hosts perm = %v", m.Perm())
	}
}
