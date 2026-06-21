package fsforge_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	fsforge "github.com/emmanuel-deloget/fsforge"
)

// sampleTree writes a small directory hierarchy and returns its root path.
func sampleTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "hello.txt"), "hello fsforge\n")
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "etc", "hosts"), "127.0.0.1 localhost\n")
	if err := os.Symlink("hello.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuilderExt2FromDir(t *testing.T) {
	src := sampleTree(t)
	out := filepath.Join(t.TempDir(), "fs.img")
	if err := fsforge.New("ext2").Size("16M").Label("test").BuildFromDir(src, out); err != nil {
		t.Fatalf("BuildFromDir: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 {
		t.Fatalf("output missing or empty: %v (size %d)", err, info.Size())
	}

	// Round-trip back to a directory and compare a couple of files.
	back := t.TempDir()
	err = fsforge.Convert(
		fsforge.Location{Kind: "ext2", Path: out},
		fsforge.Location{Kind: "dir", Path: back},
		fsforge.Options{},
	)
	if err != nil {
		t.Fatalf("Convert ext2->dir: %v", err)
	}
	assertFile(t, filepath.Join(back, "hello.txt"), "hello fsforge\n")
	assertFile(t, filepath.Join(back, "etc", "hosts"), "127.0.0.1 localhost\n")
}

func TestBuilderSquashfsFromDir(t *testing.T) {
	src := sampleTree(t)
	out := filepath.Join(t.TempDir(), "fs.sqfs")
	// squashfs is content-sized: no Size needed, output is trimmed.
	if err := fsforge.New("squashfs").BuildFromDir(src, out); err != nil {
		t.Fatalf("BuildFromDir squashfs: %v", err)
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Fatalf("squashfs output missing or empty: %v", err)
	}
}

func TestConvertDirRoundTrip(t *testing.T) {
	src := sampleTree(t)
	mid := filepath.Join(t.TempDir(), "mid.img")
	if err := fsforge.Convert(
		fsforge.Location{Kind: "dir", Path: src},
		fsforge.Location{Kind: "ext4", Path: mid},
		fsforge.Options{Size: "16M"},
	); err != nil {
		t.Fatalf("Convert dir->ext4: %v", err)
	}
	back := t.TempDir()
	if err := fsforge.Convert(
		fsforge.Location{Kind: "ext4", Path: mid},
		fsforge.Location{Kind: "dir", Path: back},
		fsforge.Options{},
	); err != nil {
		t.Fatalf("Convert ext4->dir: %v", err)
	}
	assertFile(t, filepath.Join(back, "hello.txt"), "hello fsforge\n")
}

func TestReproducibleByteIdentical(t *testing.T) {
	src := sampleTree(t)
	build := func() []byte {
		out := filepath.Join(t.TempDir(), "r.img")
		if err := fsforge.New("ext4").Reproducible(1700000000).Size("16M").BuildFromDir(src, out); err != nil {
			t.Fatalf("BuildFromDir: %v", err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if a, b := build(), build(); !bytes.Equal(a, b) {
		t.Fatal("two reproducible builds differ")
	}
}

func TestEngineForUnknown(t *testing.T) {
	if _, err := fsforge.EngineFor("ntfs", fsforge.HostDeps(), 0); err == nil {
		t.Fatal("EngineFor(ntfs) should fail")
	}
	for _, k := range []string{"ext2", "ext4", "fat", "exfat", "iso", "squashfs", "erofs", "cpio", "initramfs", "udf", "cramfs"} {
		if _, err := fsforge.EngineFor(k, fsforge.HostDeps(), 0); err != nil {
			t.Fatalf("EngineFor(%s): %v", k, err)
		}
	}
}

// TestBuilderCramfsRoundTrip builds a content-sized, trimmed cramfs image
// through the facade (exercising trimCramfs) and converts it back to a directory.
func TestBuilderCramfsRoundTrip(t *testing.T) {
	src := sampleTree(t)
	out := filepath.Join(t.TempDir(), "fs.cramfs")
	if err := fsforge.New("cramfs").Reproducible(1700000000).Label("cramvol").BuildFromDir(src, out); err != nil {
		t.Fatalf("BuildFromDir cramfs: %v", err)
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Fatalf("cramfs output missing or empty: %v", err)
	}

	back := t.TempDir()
	if err := fsforge.Convert(
		fsforge.Location{Kind: "cramfs", Path: out},
		fsforge.Location{Kind: "dir", Path: back},
		fsforge.Options{},
	); err != nil {
		t.Fatalf("Convert cramfs->dir: %v", err)
	}
	assertFile(t, filepath.Join(back, "hello.txt"), "hello fsforge\n")
	assertFile(t, filepath.Join(back, "etc", "hosts"), "127.0.0.1 localhost\n")
}

// TestBuilderUDFRoundTrip builds a content-sized, trimmed UDF image through the
// facade (exercising trimUDF) and converts it back to a directory.
func TestBuilderUDFRoundTrip(t *testing.T) {
	src := sampleTree(t)
	out := filepath.Join(t.TempDir(), "vol.udf")
	if err := fsforge.New("udf").Reproducible(1700000000).Label("FSFORGE").BuildFromDir(src, out); err != nil {
		t.Fatalf("BuildFromDir udf: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 || info.Size()%2048 != 0 {
		t.Fatalf("udf output missing or not block-aligned: size=%d err=%v", info.Size(), err)
	}

	back := t.TempDir()
	if err := fsforge.Convert(
		fsforge.Location{Kind: "udf", Path: out},
		fsforge.Location{Kind: "dir", Path: back},
		fsforge.Options{},
	); err != nil {
		t.Fatalf("Convert udf->dir: %v", err)
	}
	assertFile(t, filepath.Join(back, "hello.txt"), "hello fsforge\n")
	assertFile(t, filepath.Join(back, "etc", "hosts"), "127.0.0.1 localhost\n")
}

// TestBuilderQcow2RoundTrip builds an ext4 filesystem inside a QCOW2 container
// through the facade (exercising the .qcow2 output backend) and converts it back
// to a directory (exercising transparent QCOW2 input decoding).
func TestBuilderQcow2RoundTrip(t *testing.T) {
	src := sampleTree(t)
	out := filepath.Join(t.TempDir(), "root.qcow2")
	if !fsforge.IsQcow2Path(out) {
		t.Fatal("IsQcow2Path should recognise .qcow2")
	}
	if err := fsforge.New("ext4").Reproducible(1700000000).Size("32M").BuildFromDir(src, out); err != nil {
		t.Fatalf("BuildFromDir ext4->qcow2: %v", err)
	}
	// The container must be smaller than the 32 MiB virtual size (sparse).
	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 || info.Size() >= 32<<20 {
		t.Fatalf("qcow2 not sparse/created: size=%d err=%v", info.Size(), err)
	}

	back := t.TempDir()
	if err := fsforge.Convert(
		fsforge.Location{Kind: "ext4", Path: out},
		fsforge.Location{Kind: "dir", Path: back},
		fsforge.Options{},
	); err != nil {
		t.Fatalf("Convert ext4:qcow2->dir: %v", err)
	}
	assertFile(t, filepath.Join(back, "hello.txt"), "hello fsforge\n")
	assertFile(t, filepath.Join(back, "etc", "hosts"), "127.0.0.1 localhost\n")
}

// TestBuilderCpioRoundTrip builds a content-sized, trimmed cpio archive through
// the facade (exercising trimCpio) and converts it back to a directory.
func TestBuilderCpioRoundTrip(t *testing.T) {
	src := sampleTree(t)
	out := filepath.Join(t.TempDir(), "initramfs.cpio")
	if err := fsforge.New("cpio").Reproducible(1700000000).BuildFromDir(src, out); err != nil {
		t.Fatalf("BuildFromDir cpio: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 || info.Size()%512 != 0 {
		t.Fatalf("cpio output missing, empty or not trimmed to 512: size=%d err=%v", info.Size(), err)
	}

	back := t.TempDir()
	if err := fsforge.Convert(
		fsforge.Location{Kind: "cpio", Path: out},
		fsforge.Location{Kind: "dir", Path: back},
		fsforge.Options{},
	); err != nil {
		t.Fatalf("Convert cpio->dir: %v", err)
	}
	assertFile(t, filepath.Join(back, "hello.txt"), "hello fsforge\n")
	assertFile(t, filepath.Join(back, "etc", "hosts"), "127.0.0.1 localhost\n")
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"512":  512,
		"1K":   1 << 10,
		"64m":  64 << 20,
		"2G":   2 << 30,
		" 8M ": 8 << 20,
	}
	for in, want := range cases {
		got, err := fsforge.ParseSize(in)
		if err != nil {
			t.Fatalf("ParseSize(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
	if _, err := fsforge.ParseSize("notasize"); err == nil {
		t.Fatal("ParseSize(notasize) should fail")
	}
}

func TestMissingSizeFixed(t *testing.T) {
	// ext sinks require an explicit size.
	err := fsforge.New("ext4").BuildFromDir(sampleTree(t), filepath.Join(t.TempDir(), "x.img"))
	if err == nil {
		t.Fatal("ext4 build without Size should fail")
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(b) != want {
		t.Fatalf("%s = %q, want %q", path, b, want)
	}
}
