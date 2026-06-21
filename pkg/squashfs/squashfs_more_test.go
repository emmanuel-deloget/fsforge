package squashfs

import (
	"bytes"
	"io/fs"
	"math/rand"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/compress"
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

// TestSpecialFilesRoundTrip exercises device/fifo/socket inodes and the
// setuid/setgid/sticky permission bits through a write + Open round-trip.
func TestSpecialFilesRoundTrip(t *testing.T) {
	dev := device.NewMem(8 << 20)
	e := New(testDeps())
	img, err := e.Format(dev, image.Params{})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()

	if err := root.Mknod("chr", 0x0103, meta(fs.ModeCharDevice|0o644)); err != nil {
		t.Fatalf("Mknod chr: %v", err)
	}
	if err := root.Mknod("blk", 0x0800, meta(fs.ModeDevice|0o600)); err != nil {
		t.Fatalf("Mknod blk: %v", err)
	}
	if err := root.Mknod("pipe", 0, meta(fs.ModeNamedPipe|0o644)); err != nil {
		t.Fatalf("Mknod pipe: %v", err)
	}
	if err := root.Mknod("sock", 0, meta(fs.ModeSocket|0o644)); err != nil {
		t.Fatalf("Mknod sock: %v", err)
	}
	// A setuid/setgid/sticky regular file to cover unixPerm and sqMode bits.
	if _, err := root.Create("suid", tree.Bytes("x"), meta(0o755|fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)); err != nil {
		t.Fatalf("Create suid: %v", err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r := opened.(rootNoder).RootNode()

	chr := find(r, "chr")
	if chr == nil || chr.Mode&fs.ModeCharDevice == 0 || chr.Rdev != 0x0103 {
		t.Errorf("chr device lost: %+v", chr)
	}
	blk := find(r, "blk")
	if blk == nil || blk.Mode&fs.ModeDevice == 0 || blk.Mode&fs.ModeCharDevice != 0 {
		t.Errorf("blk device lost: %+v", blk)
	}
	if p := find(r, "pipe"); p == nil || p.Mode&fs.ModeNamedPipe == 0 {
		t.Errorf("fifo lost: %+v", p)
	}
	if s := find(r, "sock"); s == nil || s.Mode&fs.ModeSocket == 0 {
		t.Errorf("socket lost: %+v", s)
	}
	suid := find(r, "suid")
	if suid == nil || suid.Mode&fs.ModeSetuid == 0 || suid.Mode&fs.ModeSetgid == 0 || suid.Mode&fs.ModeSticky == 0 {
		t.Errorf("setuid/setgid/sticky lost: %v", suid.Mode)
	}
}

// TestMultiBlockIncompressible covers the multi-block read path and the
// uncompressed-block branch: random data does not shrink, so each block is
// stored raw and spans several block boundaries with a small block size.
func TestMultiBlockIncompressible(t *testing.T) {
	dev := device.NewMem(8 << 20)
	e := New(testDeps(), WithCompressor(compress.Zlib{}), WithBlockSize(4096))
	img, err := e.Format(dev, image.Params{})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	rng := rand.New(rand.NewSource(1))
	payload := make([]byte, 4096*3+777)
	rng.Read(payload)
	if _, err := img.Root().Create("rand.bin", tree.Bytes(payload), meta(0o644)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	opened, err := New(testDeps(), WithBlockSize(4096)).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	src := find(opened.(rootNoder).RootNode(), "rand.bin").Content
	if src.Size() != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", src.Size(), len(payload))
	}
	got := make([]byte, len(payload))
	if _, err := src.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("multi-block round-trip mismatch")
	}
	// A partial read straddling a block boundary.
	mid := make([]byte, 100)
	if _, err := src.ReadAt(mid, 4000); err != nil {
		t.Fatalf("ReadAt mid: %v", err)
	}
	if !bytes.Equal(mid, payload[4000:4100]) {
		t.Fatal("straddling read mismatch")
	}
}

func TestFormatRejectsBadBlockSize(t *testing.T) {
	for _, bs := range []uint32{1000, 2048} { // not power-of-two>=4096
		_, err := New(testDeps(), WithBlockSize(bs)).Format(device.NewMem(1<<20), image.Params{})
		if err == nil {
			t.Errorf("block size %d should be rejected", bs)
		}
	}
}

func TestOpenBadImage(t *testing.T) {
	// A device of zeros has no valid magic.
	_, err := New(testDeps()).Open(device.NewMem(1 << 20))
	if err == nil {
		t.Fatal("Open of a blank device should fail")
	}
	if err.Error() == "" {
		t.Fatal("error message should be non-empty")
	}
}

func TestOpenedImageCannotRefinalize(t *testing.T) {
	dev := device.NewMem(8 << 20)
	buildSample(t, dev)
	opened, err := New(testDeps()).Open(dev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := opened.Finalize(); err == nil {
		t.Fatal("re-finalizing an opened squashfs image should fail")
	}
}
