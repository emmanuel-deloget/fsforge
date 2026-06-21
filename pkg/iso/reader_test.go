package iso

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
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
	b := make([]byte, n.Content.Size())
	if _, err := n.Content.ReadAt(b, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt: %v", err)
	}
	return b
}

func TestOpenRoundTrip(t *testing.T) {
	dev := device.NewMem(16 << 20)
	img, err := New(testDeps()).Format(dev, image.Params{Label: "FSFORGE"})
	if err != nil {
		t.Fatal(err)
	}
	root := img.Root()
	etc, _ := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o600))
	root.Create("big.txt", tree.Bytes(bytes.Repeat([]byte("iso\n"), 2000)), meta(0o644))
	root.Symlink("link", "etc/hosts", meta(fs.ModeSymlink|0o777))
	root.Mknod("null", 0x0103, meta(fs.ModeCharDevice|0o666))
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r := opened.(rootNoder).RootNode()

	// Rock Ridge name + nested file + preserved permissions.
	hosts := find(find(r, "etc"), "hosts")
	if hosts == nil {
		t.Fatal("etc/hosts missing")
	}
	if got := string(readAll(t, hosts)); got != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q", got)
	}
	if hosts.Mode.Perm() != 0o600 {
		t.Errorf("etc/hosts perm = %o, want 600", hosts.Mode.Perm())
	}

	// Multi-sector file.
	big := find(r, "big.txt")
	if got := readAll(t, big); !bytes.Equal(got, bytes.Repeat([]byte("iso\n"), 2000)) {
		t.Errorf("big.txt mismatch: %d bytes", len(got))
	}

	// Symlink target via SL, device via PN.
	if ln := find(r, "link"); ln == nil || ln.Mode&fs.ModeSymlink == 0 || ln.Link != "etc/hosts" {
		t.Errorf("symlink not recovered: %+v", ln)
	}
	if dn := find(r, "null"); dn == nil || dn.Mode&fs.ModeCharDevice == 0 || dn.Rdev != 0x0103 {
		t.Errorf("device not recovered: %+v", dn)
	}
}

func TestOpenRejectsNonISO(t *testing.T) {
	if _, err := New(testDeps()).Open(device.NewMem(64 << 10)); err == nil {
		t.Fatal("Open of a blank device should fail (no CD001)")
	}
}
