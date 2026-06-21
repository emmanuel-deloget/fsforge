package fsforge

import (
	"os"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/qcow2"
)

// IsQcow2Path reports whether an output path selects QCOW2 container output by
// its extension (.qcow2 or .qcow). QCOW2 is a disk-image container, not a
// filesystem: it wraps whatever the chosen engine (or `fsforge disk`) writes.
func IsQcow2Path(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".qcow2") || strings.HasSuffix(p, ".qcow")
}

// outputBackend wraps the freshly created file f as the device a build writes
// to, plus a finalize step run after the engine's Finalize: for QCOW2 it flushes
// the container metadata, otherwise it trims content-sized raw images. virtual
// is the device size in bytes (a fixed size, or a content-sized estimate).
func outputBackend(fstype, outPath string, f *os.File, virtual int64) (device.Device, func() error, error) {
	if IsQcow2Path(outPath) {
		w, err := qcow2.NewWriter(f, virtual)
		if err != nil {
			return nil, nil, err
		}
		return w, w.Finalize, nil
	}
	if err := f.Truncate(virtual); err != nil {
		return nil, nil, err
	}
	return device.NewFile(f, virtual), func() error { return trim(fstype, f) }, nil
}

// inputDevice presents an on-disk image as a device, transparently decoding a
// QCOW2 container when f starts with the QCOW2 magic, so any engine can Open a
// filesystem stored inside one.
func inputDevice(f *os.File) (device.Device, error) {
	var magic [4]byte
	if _, err := f.ReadAt(magic[:], 0); err == nil && qcow2.IsQcow2(magic[:]) {
		return qcow2.Open(f)
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return device.NewFile(f, info.Size()), nil
}
