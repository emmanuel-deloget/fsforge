package fsforge_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// TestBuildRootfsFromUnprivilegedCheckout is the case the whole spec mechanism
// exists for, end to end: a source tree such as a CI checkout would hold —
// owned by whoever ran the build, no device nodes, no setuid bit — plus a file
// describing what the image actually needs, producing a rootfs that has all of
// it.
//
// Nothing here needs privileges, which is the point. Under `id -u` != 0 the
// source tree cannot be chowned to root and mknod would fail; the image still
// comes out with uid 0 and /dev/console.
func TestBuildRootfsFromUnprivilegedCheckout(t *testing.T) {
	src := t.TempDir()
	for _, dir := range []string{"etc", "bin", "usr/share"} {
		if err := os.MkdirAll(filepath.Join(src, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "etc", "hostname"), []byte("forge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "ping"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	spec := filepath.Join(t.TempDir(), "rootfs.mtree")
	if err := os.WriteFile(spec, []byte(`#mtree
# Everything belongs to root, whoever built it.
/set uid=0 gid=0
.               type=dir mode=0755
./etc           type=dir mode=0755
./etc/hostname  type=file mode=0644
./bin           type=dir mode=0755
./bin/ping      type=file mode=4755
./usr           type=dir mode=0755
./usr/share     type=dir mode=0755
./dev           type=dir mode=0755
./dev/console   type=char mode=0600 device=native,5,1
./dev/null      type=char mode=0666 device=native,1,3
./tmp           type=dir mode=01777
./var/run       type=link link=../run mode=0777
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "rootfs.img")
	if err := fsforge.New("ext4").
		Reproducible(1600000000).
		Size("16M").
		Label("root").
		Spec(spec).
		BuildFromDir(src, out); err != nil {
		t.Fatalf("build: %v", err)
	}

	root := readBack(t, "ext4", out)

	// Ownership the checkout could not have.
	for _, p := range []string{"etc", "etc/hostname", "bin/ping", "dev/console"} {
		n := at(root, p)
		if n == nil {
			t.Fatalf("%s missing from the image", p)
		}
		if n.UID != 0 || n.GID != 0 {
			t.Errorf("%s is owned by %d:%d, want 0:0", p, n.UID, n.GID)
		}
	}
	// A setuid bit, which most CI filesystems drop.
	if n := at(root, "bin/ping"); n.Mode&fs.ModeSetuid == 0 {
		t.Errorf("bin/ping lost its setuid bit: %v", n.Mode)
	}
	// Device nodes, which need CAP_MKNOD to create on the host.
	console := at(root, "dev/console")
	if console == nil || console.Mode&fs.ModeCharDevice == 0 {
		t.Fatalf("dev/console is not a character device: %v", console)
	}
	if console.Rdev != 5<<8|1 {
		t.Errorf("dev/console rdev = %#x, want %#x", console.Rdev, 5<<8|1)
	}
	if n := at(root, "dev/null"); n == nil || n.Rdev != 1<<8|3 {
		t.Errorf("dev/null rdev wrong: %v", n)
	}
	// A sticky directory and a symlink, neither of which was in the source.
	if n := at(root, "tmp"); n == nil || n.Mode&fs.ModeSticky == 0 {
		t.Errorf("tmp is not sticky: %v", n)
	}
	if n := at(root, "var/run"); n == nil || n.Link != "../run" {
		t.Errorf("var/run is not the expected symlink: %v", n)
	}
	// The files themselves still came from the checkout.
	if n := at(root, "etc/hostname"); n == nil || n.Content == nil || n.Content.Size() != 6 {
		t.Errorf("etc/hostname lost its contents: %v", n)
	}
}

// TestSpecKeepsBuildsReproducible checks the spec does not reintroduce host
// state: two builds of the same tree and spec must be identical.
func TestSpecKeepsBuildsReproducible(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(t.TempDir(), "s.mtree")
	if err := os.WriteFile(spec, []byte("/set uid=0 gid=0\n./file type=file mode=0600\n"+
		"./dev/console type=char mode=0600 device=native,5,1 time=1600000000.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func() []byte {
		out := filepath.Join(t.TempDir(), "img.sqsh")
		if err := fsforge.New("squashfs").Reproducible(1600000000).Spec(spec).
			BuildFromDir(src, out); err != nil {
			t.Fatalf("build: %v", err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if a, b := build(), build(); string(a) != string(b) {
		t.Error("two builds with the same spec differ")
	}
}

func TestSpecErrorsReachTheCaller(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "img.sqsh")

	if err := fsforge.New("squashfs").Spec(filepath.Join(t.TempDir(), "absent.mtree")).
		BuildFromDir(src, out); err == nil {
		t.Error("a missing spec file should fail the build")
	}

	bad := filepath.Join(t.TempDir(), "bad.mtree")
	if err := os.WriteFile(bad, []byte("./x type=nonesuch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := fsforge.New("squashfs").Spec(bad).BuildFromDir(src, out)
	if err == nil {
		t.Fatal("an unparsable spec should fail the build")
	}
	if !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), bad) {
		t.Errorf("the error should name the file and the line: %v", err)
	}
}

// readBack opens a written image and returns its tree.
func readBack(t *testing.T, fstype, path string) *image.Node {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := fsforge.EngineFor(fstype, fsforge.HostDeps(), 0)
	if err != nil {
		t.Fatal(err)
	}
	img, err := eng.Open(device.NewFile(f, fi.Size()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return img.(interface{ RootNode() *image.Node }).RootNode()
}

// at walks a slash-separated path from a root node.
func at(n *image.Node, p string) *image.Node {
	for _, part := range splitPath(p) {
		var next *image.Node
		for _, e := range n.Children {
			if e.Name == part {
				next = e.Node
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
}

// TestSpecNodesGetASensibleTime pins a contract the engines have to honour:
// tree.Meta documents a zero ModTime as "resolve from the injected clock", and
// a specification creates nodes without one. ext and squashfs used to convert
// the zero value straight to a uint32, which put /dev/console in 2042 — an
// image e2fsck accepts and no reader would question.
func TestSpecNodesGetASensibleTime(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(t.TempDir(), "s.mtree")
	if err := os.WriteFile(spec, []byte(
		"./dev type=dir mode=0755\n./dev/console type=char mode=0600 device=native,5,1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const epoch = 1600000000
	for _, fsType := range []string{"ext4", "squashfs"} {
		t.Run(fsType, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "img")
			size := ""
			if fsType == "ext4" {
				size = "16M"
			}
			if err := fsforge.New(fsType).Reproducible(epoch).Size(size).Spec(spec).
				BuildFromDir(src, out); err != nil {
				t.Fatalf("build: %v", err)
			}
			n := at(readBack(t, fsType, out), "dev/console")
			if n == nil {
				t.Fatal("dev/console missing")
			}
			if got := n.ModTime.Unix(); got != epoch {
				t.Errorf("dev/console mtime = %d (%s), want the injected clock's %d",
					got, n.ModTime.UTC(), int64(epoch))
			}
		})
	}
}
