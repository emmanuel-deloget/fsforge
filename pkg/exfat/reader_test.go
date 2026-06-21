package exfat

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

type rootNoder interface{ RootNode() *image.Node }

func find(n *image.Node, name string) *image.Node {
	for _, e := range n.Children {
		if e.Name == name {
			return e.Node
		}
	}
	return nil
}

func readAll(t *testing.T, n *image.Node) []byte {
	t.Helper()
	if n.Content == nil {
		return nil
	}
	b := make([]byte, n.Content.Size())
	if _, err := n.Content.ReadAt(b, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt: %v", err)
	}
	return b
}

func TestOpenRoundTrip(t *testing.T) {
	dev := device.NewMem(64 << 20)
	buildSample(t, dev)

	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	root := opened.(rootNoder).RootNode()

	// Top-level files.
	if got := string(readAll(t, find(root, "short.txt"))); got != "hi\n" {
		t.Errorf("short.txt = %q, want \"hi\\n\"", got)
	}
	if got := string(readAll(t, find(root, "a long file name here.dat"))); got != "hello exfat\n" {
		t.Errorf("long-name file = %q", got)
	}

	// Subdirectory with a multi-cluster file.
	sub := find(root, "subdir")
	if sub == nil || !sub.Mode.IsDir() {
		t.Fatalf("subdir missing or not a directory: %+v", sub)
	}
	big := find(sub, "file in subdir.txt")
	if big == nil {
		t.Fatal("subdir file missing")
	}
	want := bytes.Repeat([]byte("X"), 200000)
	if got := readAll(t, big); !bytes.Equal(got, want) {
		t.Errorf("subdir file mismatch: %d bytes vs %d", len(got), len(want))
	}
}

func TestOpenRejectsBadVolume(t *testing.T) {
	if _, err := New(testDeps()).Open(device.NewMem(1 << 20)); err == nil {
		t.Fatal("Open of a blank device should fail (bad boot signature)")
	}
}

// A partial read straddling a cluster boundary must return the right bytes.
func TestOpenPartialRead(t *testing.T) {
	dev := device.NewMem(64 << 20)
	buildSample(t, dev)
	opened, _ := New(testDeps()).Open(dev)
	root := opened.(rootNoder).RootNode()
	big := find(find(root, "subdir"), "file in subdir.txt")

	// exFAT clusters here are 32 KiB; read across the first boundary.
	p := make([]byte, 100)
	if _, err := big.Content.ReadAt(p, 32*1024-50); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(p, bytes.Repeat([]byte("X"), 100)) {
		t.Errorf("straddling read mismatch: %q", p)
	}
}
