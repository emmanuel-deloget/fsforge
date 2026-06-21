package ext

import (
	"io/fs"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// TestSpecialFiles round-trips device nodes and a fifo through both ext2 and
// ext4, covering the special-inode layout (buildSpecial) and the reader's
// device/fifo mode mapping.
func TestSpecialFiles(t *testing.T) {
	cases := []struct {
		name string
		make func() *Engine
	}{
		{"ext2", func() *Engine { return NewExt2(testDeps()) }},
		{"ext4", func() *Engine { return NewExt4(testDeps()) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dev := device.NewMem(8 << 20)
			img, err := tc.make().Format(dev, image.Params{Label: "spec"})
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			root := img.Root()
			if err := root.Mknod("cdev", 0x0103, meta(fs.ModeCharDevice|0o600)); err != nil {
				t.Fatal(err)
			}
			if err := root.Mknod("bdev", 0x0800, meta(fs.ModeDevice|0o660)); err != nil {
				t.Fatal(err)
			}
			if err := root.Mknod("fifo", 0, meta(fs.ModeNamedPipe|0o644)); err != nil {
				t.Fatal(err)
			}
			if err := img.Finalize(); err != nil {
				t.Fatalf("Finalize: %v", err)
			}

			opened, err := tc.make().Open(dev)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			r := opened.(rootNoder).RootNode()

			cdev := childByName(r, "cdev")
			if cdev == nil || cdev.Mode&fs.ModeCharDevice == 0 || cdev.Rdev != 0x0103 {
				t.Errorf("char device wrong: %+v", cdev)
			}
			bdev := childByName(r, "bdev")
			if bdev == nil || bdev.Mode&fs.ModeDevice == 0 || bdev.Mode&fs.ModeCharDevice != 0 {
				t.Errorf("block device wrong: %+v", bdev)
			}
			if p := childByName(r, "fifo"); p == nil || p.Mode&fs.ModeNamedPipe == 0 {
				t.Errorf("fifo wrong: %+v", p)
			}
		})
	}
}
