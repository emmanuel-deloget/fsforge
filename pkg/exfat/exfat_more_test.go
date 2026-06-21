package exfat

import (
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func TestNewNilDeps(t *testing.T) {
	// New must default nil Clock/UUID without panicking, and Format must work.
	e := New(image.Deps{})
	dev := device.NewMem(32 << 20)
	img, err := e.Format(dev, image.Params{Label: "FSFORGE"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if _, err := img.Root().Create("f.txt", tree.Bytes("hi"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if err := img.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}
