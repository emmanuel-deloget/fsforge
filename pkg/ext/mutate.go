package ext

import (
	"io"
	"os"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// finalizeStaged lays the mutated tree out onto a scratch device, then copies it
// over the target. During layout the engine reads file contents from the
// original device (through the lazy fileSources built by Open) and writes the
// new image to scratch, so no still-needed block is overwritten mid-flight.
func (img *ext2Image) finalizeStaged() error {
	scratch, cleanup, err := newScratch(img.dev.Size())
	if err != nil {
		return err
	}
	defer cleanup()

	if err := img.newLayouter(scratch).run(img.RootNode()); err != nil {
		return err
	}
	return copyDevice(img.dev, scratch)
}

// newScratch returns a temporary file-backed device of the given size and a
// cleanup func. A file (not memory) keeps the staging cost off the heap.
func newScratch(size int64) (device.Device, func(), error) {
	f, err := os.CreateTemp("", "fsforge-scratch-*.img")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() {
		name := f.Name()
		f.Close()
		os.Remove(name)
	}
	if err := f.Truncate(size); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return device.NewFile(f, size), cleanup, nil
}

// copyDevice streams src over dst (same size) in 1 MiB chunks.
func copyDevice(dst, src device.Device) error {
	const chunk = 1 << 20
	size := src.Size()
	buf := make([]byte, chunk)
	for off := int64(0); off < size; {
		n := int64(chunk)
		if size-off < n {
			n = size - off
		}
		if _, err := src.ReadAt(buf[:n], off); err != nil && err != io.EOF {
			return err
		}
		if _, err := dst.WriteAt(buf[:n], off); err != nil {
			return err
		}
		off += n
	}
	return nil
}
