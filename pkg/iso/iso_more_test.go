package iso

import (
	"io/fs"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// TestSpecialFilesAndNames exercises Rock Ridge device/fifo encoding (rrMode)
// and the ISO9660 name sanitisation for mixed-case and long names.
func TestSpecialFilesAndNames(t *testing.T) {
	dev := device.NewMem(16 << 20)
	img, err := New(testDeps()).Format(dev, image.Params{Label: "FSFORGE"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	root := img.Root()
	if err := root.Mknod("null", 0x0103, meta(fs.ModeCharDevice|0o666)); err != nil {
		t.Fatal(err)
	}
	if err := root.Mknod("sda", 0x0800, meta(fs.ModeDevice|0o660)); err != nil {
		t.Fatal(err)
	}
	if err := root.Mknod("apipe", 0, meta(fs.ModeNamedPipe|0o644)); err != nil {
		t.Fatal(err)
	}
	// Mixed-case, dotted and long names force ISO9660 sanitisation while Rock
	// Ridge preserves the original via NM entries.
	if _, err := root.Create("MixedCase.Name.Ext", tree.Bytes("x"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Create("this is a very long file name that exceeds iso limits.txt", tree.Bytes("y"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func TestOpenUnsupported(t *testing.T) {
	if _, err := New(testDeps()).Open(device.NewMem(1 << 20)); err == nil {
		t.Fatal("ISO Open should report it is unsupported")
	}
}

func TestNewNilClock(t *testing.T) {
	e := New(image.Deps{}) // nil Clock must be defaulted
	dev := device.NewMem(16 << 20)
	img, err := e.Format(dev, image.Params{})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if _, err := img.Root().Create("f", tree.Bytes("z"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}
