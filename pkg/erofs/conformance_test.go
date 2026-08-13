//go:build conformance

package erofs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// writeImage builds the shared sample into a memory device and writes the bytes
// the engine actually used (superblock `blocks` * blockSize) to path.
func writeImage(t *testing.T, path string) {
	t.Helper()
	dev := device.NewMem(32 << 20)
	buildSample(t, dev)
	b := dev.Bytes()
	sb, err := parseSuperblock(b[superOffset : superOffset+superSize])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b[:int(sb.blocks)*blockSize], 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFsckErofs validates a fsforge-written image structurally with the real
// fsck.erofs (host binary or erofs-utils container).
// Run: go test -tags conformance ./pkg/erofs/
func TestFsckErofs(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "test.erofs")
	writeImage(t, imgPath)

	out, err := conformance.FsckErofs(imgPath)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("fsck.erofs unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("fsck.erofs reported problems: %v\n%s", err, out)
	}
}

// TestErofsExtract extracts a fsforge-written image with fsck.erofs --extract
// and checks file contents and a symlink survived. The sample here avoids
// device nodes so extraction works under a rootless container.
func TestErofsExtract(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "extract.erofs")

	dev := device.NewMem(16 << 20)
	img, err := New(testDeps()).Format(dev, image.Params{Label: "fsforge"})
	if err != nil {
		t.Fatal(err)
	}
	root := img.Root()
	etc, _ := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644))
	root.Create("big.bin", tree.Bytes(sampleData(2000)), meta(0o644))
	root.Symlink("link", "etc/hosts", meta(fs.ModeSymlink|0o777))
	if err := img.Finalize(); err != nil {
		t.Fatal(err)
	}
	b := dev.Bytes()
	sb, _ := parseSuperblock(b[superOffset : superOffset+superSize])
	if err := os.WriteFile(imgPath, b[:int(sb.blocks)*blockSize], 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "extracted")
	combined, err := conformance.ErofsExtract(imgPath, out)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("fsck.erofs unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("fsck.erofs --extract failed: %v\n%s", err, combined)
	}

	if got, _ := os.ReadFile(filepath.Join(out, "etc/hosts")); string(got) != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "big.bin")); string(got) != string(sampleData(2000)) {
		t.Errorf("big.bin content mismatch (%d bytes)", len(got))
	}
	if target, err := os.Readlink(filepath.Join(out, "link")); err != nil || target != "etc/hosts" {
		t.Errorf("symlink = %q (%v)", target, err)
	}
}

// TestReadMkfsErofs builds an image with the real mkfs.erofs (compact inodes,
// inline tails) and reads it back with our parser.
// Run: go test -tags conformance ./pkg/erofs/
func TestReadMkfsErofs(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(filepath.Join(src, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "readme.txt"), []byte("hello erofs\n"), 0o644)
	os.WriteFile(filepath.Join(src, "etc", "hosts"), []byte("127.0.0.1 localhost\n"), 0o644)
	os.WriteFile(filepath.Join(src, "big.bin"), sampleData(2000), 0o644)
	if err := os.Symlink("etc/hosts", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	// A wide directory to force multi-block, tool-written directory layout.
	wide := filepath.Join(src, "wide")
	os.MkdirAll(wide, 0o755)
	for i := 0; i < 300; i++ {
		name := fmt.Sprintf("file-%04d", i)
		os.WriteFile(filepath.Join(wide, name), []byte(name), 0o644)
	}

	imgPath := filepath.Join(parent, "out.erofs")
	combined, err := conformance.MakeErofs(src, imgPath)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("mkfs.erofs unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("mkfs.erofs failed: %v\n%s", err, combined)
	}

	f, err := os.Open(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()

	opened, err := New(testDeps()).Open(device.NewFile(f, info.Size()))
	if err != nil {
		t.Fatalf("Open mkfs.erofs image: %v", err)
	}
	root := opened.(interface{ RootNode() *image.Node }).RootNode()

	if got := string(readAll(t, find(root, "readme.txt").Content)); got != "hello erofs\n" {
		t.Errorf("readme.txt = %q", got)
	}
	if got := string(readAll(t, find(find(root, "etc"), "hosts").Content)); got != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q", got)
	}
	if got := readAll(t, find(root, "big.bin").Content); string(got) != string(sampleData(2000)) {
		t.Errorf("big.bin mismatch (%d bytes)", len(got))
	}
	if ln := find(root, "link"); ln == nil || ln.Mode&fs.ModeSymlink == 0 || ln.Link != "etc/hosts" {
		t.Errorf("symlink not recovered from mkfs.erofs image: %+v", ln)
	}
	w := find(root, "wide")
	if w == nil || len(w.Children) != 300 {
		t.Fatalf("wide dir: got %d children", len(w.Children))
	}
}

// TestXattrConformance puts inline extended attributes in front of the real
// fsck.erofs. The structural risk is the inode layout rather than the entries
// themselves: the attribute area sits between one inode core and the next, so a
// miscounted i_xattr_icount makes fsck read the following inode from the middle
// of an attribute.
func TestXattrConformance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		xattrs map[string][]byte
	}{{
		name:   "one small",
		xattrs: map[string][]byte{"security.capability": []byte("0123456789abcdefghij")},
	}, {
		name: "several",
		xattrs: map[string][]byte{
			"security.selinux":    []byte("system_u:object_r:etc_t:s0\x00"),
			"security.capability": []byte("0123456789abcdefghij"),
			"user.comment":        []byte(strings.Repeat("x", 200)),
			"trusted.overlay.foo": []byte("bar"),
		},
	}, {
		name:   "empty value",
		xattrs: map[string][]byte{"user.empty": nil, "user.one": []byte("1")},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			imgPath := filepath.Join(tmp, "xattr.erofs")

			dev := device.NewMem(16 << 20)
			img, err := New(testDeps()).Format(dev, image.Params{Label: "fsforge"})
			if err != nil {
				t.Fatal(err)
			}
			root := img.Root()
			// Attributes on several node kinds, and on more than one inode, so a
			// layout error shows up as a misread neighbour.
			m := meta(0o644)
			m.Xattrs = tc.xattrs
			for _, name := range []string{"a", "b", "c"} {
				if _, err := root.Create(name, tree.Bytes("payload\n"), m); err != nil {
					t.Fatal(err)
				}
			}
			dm := meta(fs.ModeDir | 0o755)
			dm.Xattrs = tc.xattrs
			sub, err := root.Mkdir("dir", dm)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sub.Create("nested", tree.Bytes("n"), m); err != nil {
				t.Fatal(err)
			}
			sm := meta(fs.ModeSymlink | 0o777)
			sm.Xattrs = tc.xattrs
			if err := root.Symlink("link", "a", sm); err != nil {
				t.Fatal(err)
			}
			if err := img.Finalize(); err != nil {
				t.Fatal(err)
			}

			b := dev.Bytes()
			sb, err := parseSuperblock(b[superOffset : superOffset+superSize])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(imgPath, b[:int(sb.blocks)*blockSize], 0o644); err != nil {
				t.Fatal(err)
			}

			out, err := conformance.FsckErofs(imgPath)
			if errors.Is(err, conformance.ErrUnavailable) {
				t.Skip("fsck.erofs unavailable (no host binary or container runtime)")
			}
			if err != nil {
				t.Fatalf("fsck.erofs reported problems: %v\n%s", err, out)
			}
		})
	}
}
