package cramfs

import (
	"bytes"
	"fmt"
	"io/fs"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

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
	if got := readAll(t, find(root, "data.bin").Content); !bytes.Equal(got, sampleBytes(10000)) {
		t.Errorf("data.bin mismatch: %d bytes", len(got))
	}
	if e := find(root, "empty"); e == nil || e.Content == nil || e.Content.Size() != 0 {
		t.Errorf("empty file lost")
	}
	if ln := find(root, "link"); ln == nil || ln.Mode&fs.ModeSymlink == 0 || ln.Link != "etc/hosts" {
		t.Errorf("symlink lost: %+v", ln)
	}
	if dn := find(root, "null"); dn == nil || dn.Mode&fs.ModeCharDevice == 0 || dn.Rdev != 0x0103 {
		t.Errorf("device lost: %+v", dn)
	}
	if d := find(find(find(root, "a"), "b"), "deep.txt"); d == nil || string(readAll(t, d.Content)) != "deep\n" {
		t.Errorf("nested file lost")
	}
}

func TestManyEntries(t *testing.T) {
	dev := device.NewMem(16 << 20)
	img, _ := New(testDeps()).Format(dev, image.Params{})
	root := img.Root()
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("file-%04d", i)
		root.Create(name, tree.Bytes([]byte(name)), meta(0o644))
	}
	if err := img.Finalize(); err != nil {
		t.Fatal(err)
	}
	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatal(err)
	}
	root2 := opened.(interface{ RootNode() *image.Node }).RootNode()
	if len(root2.Children) != 200 {
		t.Fatalf("got %d children", len(root2.Children))
	}
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("file-%04d", i)
		if c := find(root2, name); c == nil || string(readAll(t, c.Content)) != name {
			t.Fatalf("%s lost", name)
		}
	}
}
