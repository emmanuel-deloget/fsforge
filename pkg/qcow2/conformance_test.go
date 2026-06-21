//go:build conformance

package qcow2

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
)

// expectedRaw builds, in memory, the raw disk the pattern should decode to.
func expectedRaw(regions map[int64][]byte) []byte {
	raw := make([]byte, virtual)
	for off, data := range regions {
		copy(raw[off:], data)
	}
	return raw
}

// buildImage writes a pattern into a QCOW2 file on disk and returns the pattern.
func buildImage(t *testing.T, path string) map[int64][]byte {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w, err := NewWriter(f, virtual)
	if err != nil {
		t.Fatal(err)
	}
	regions := writePattern(t, w)
	if err := w.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return regions
}

// TestQemuImgCheck validates a fsforge-written image with `qemu-img check`.
// Run: go test -tags conformance ./pkg/qcow2/
func TestQemuImgCheck(t *testing.T) {
	tmp := t.TempDir()
	img := filepath.Join(tmp, "test.qcow2")
	buildImage(t, img)

	out, err := conformance.QemuImgCheck(img)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("qemu-img unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("qemu-img check reported problems: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No errors") {
		t.Errorf("unexpected qemu-img check output:\n%s", out)
	}
}

// TestQemuImgToRaw converts our image to raw with qemu-img and checks it decodes
// to exactly the bytes we wrote.
func TestQemuImgToRaw(t *testing.T) {
	tmp := t.TempDir()
	img := filepath.Join(tmp, "test.qcow2")
	regions := buildImage(t, img)

	raw := filepath.Join(tmp, "test.raw")
	out, err := conformance.QemuImgToRaw(img, raw)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("qemu-img unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("qemu-img convert failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(got)) != virtual {
		t.Fatalf("raw size = %d, want %d", len(got), virtual)
	}
	if !bytes.Equal(got, expectedRaw(regions)) {
		t.Errorf("qemu-img decoded our qcow2 to different bytes than written")
	}
}

// TestReadQemuQcow2 builds a QCOW2 with qemu-img from a raw image and reads it
// back with our Reader.
func TestReadQemuQcow2(t *testing.T) {
	tmp := t.TempDir()
	regions := map[int64][]byte{
		0:               bytes.Repeat([]byte("RAW0"), 64),
		clusterSize + 7: []byte("a tool-written payload"),
		4 * clusterSize: bytes.Repeat([]byte{0x5A}, clusterSize),
		virtual - 256:   bytes.Repeat([]byte("tail"), 64),
	}
	rawData := make([]byte, virtual)
	for off, data := range regions {
		copy(rawData[off:], data)
	}
	raw := filepath.Join(tmp, "src.raw")
	if err := os.WriteFile(raw, rawData, 0o644); err != nil {
		t.Fatal(err)
	}

	img := filepath.Join(tmp, "src.qcow2")
	out, err := conformance.MakeQcow2FromRaw(raw, img)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("qemu-img unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("qemu-img convert to qcow2 failed: %v\n%s", err, out)
	}

	f, err := os.Open(img)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := Open(f)
	if err != nil {
		t.Fatalf("Open qemu qcow2: %v", err)
	}
	if r.Size() != virtual {
		t.Fatalf("size = %d", r.Size())
	}
	got := make([]byte, virtual)
	if _, err := r.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, rawData) {
		t.Errorf("our Reader decoded qemu's qcow2 differently from the source raw")
	}
}
