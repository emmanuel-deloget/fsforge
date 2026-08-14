package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The CLI is thin by design, so these tests aim at the two things a thin shell
// can still get wrong: turning arguments into the library's inputs, and turning
// the library's failures into an exit status. Each command is also run once for
// real, because "the flags parsed" says nothing about whether an image came out.

// quiet swallows stdout and stderr for the duration of f, so a passing run does
// not bury the test output in usage text. It returns what was written to stderr.
func quiet(t *testing.T, f func()) string {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW

	done := make(chan string, 2)
	drain := func(r *os.File) {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}
	go drain(outR)
	go drain(errR)

	f()

	os.Stdout, os.Stderr = oldOut, oldErr
	outW.Close()
	errW.Close()
	a, b := <-done, <-done
	// The goroutines finish in either order; stderr is whichever holds usage.
	if strings.Contains(a, "usage:") || strings.Contains(a, "fsforge:") {
		return a
	}
	return b
}

// sampleDir is a small source tree to build images from.
func sampleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "etc", "hosts"), []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunExitStatus(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no arguments", nil, 2},
		{"unknown command", []string{"frobnicate"}, 2},
		{"help", []string{"help"}, 0},
		{"help flag", []string{"-h"}, 0},
		{"long help flag", []string{"--help"}, 0},
		{"missing required flags", []string{"mkfs"}, 1},
		{"bad flag", []string{"mkfs", "-nonesuch"}, 1},
		{"convert without arguments", []string{"convert"}, 1},
		{"disk without arguments", []string{"disk"}, 1},
		{"oci-add-layer without arguments", []string{"oci-add-layer"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got int
			quiet(t, func() { got = run(tc.args) })
			if got != tc.want {
				t.Errorf("run(%q) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

// TestUsageMentionsEveryCommand keeps the help text honest: a command the shell
// dispatches but does not document is one nobody will find.
func TestUsageMentionsEveryCommand(t *testing.T) {
	text := quiet(t, usage)
	for _, cmd := range []string{"mkfs", "convert", "disk", "oci-add-layer"} {
		if !strings.Contains(text, cmd) {
			t.Errorf("usage does not mention %q", cmd)
		}
	}
}

func TestParseLoc(t *testing.T) {
	loc, err := parseLoc("ext4:/tmp/root.img")
	if err != nil {
		t.Fatal(err)
	}
	if loc.Kind != "ext4" || loc.Path != "/tmp/root.img" {
		t.Errorf("parsed %+v", loc)
	}
	// A Windows-style path keeps its colon in the path, the split being on the
	// first one only.
	loc, err = parseLoc("dir:C:/work/rootfs")
	if err != nil {
		t.Fatal(err)
	}
	if loc.Kind != "dir" || loc.Path != "C:/work/rootfs" {
		t.Errorf("parsed %+v", loc)
	}
	if _, err := parseLoc("nocolon"); err == nil {
		t.Error("a location without a colon should be refused")
	}
}

func TestPartFlagsParsing(t *testing.T) {
	var p partFlags
	if err := p.Set("esp:fat:./esp:64M"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("root:ext4:./rootfs:rest"); err != nil {
		t.Fatal(err)
	}
	if len(p) != 2 {
		t.Fatalf("got %d partitions, want 2", len(p))
	}
	if p[0].role != "esp" || p[0].fstype != "fat" || p[0].source != "./esp" || p[0].size != 64<<20 {
		t.Errorf("first partition parsed as %+v", p[0])
	}
	// "rest" means "whatever is left", carried as a zero size.
	if p[1].size != 0 {
		t.Errorf("rest should parse to size 0, got %d", p[1].size)
	}
	if p.String() != "" {
		t.Error("String is the flag package's placeholder and should stay empty")
	}

	for _, bad := range []string{"too:few:fields", "esp:fat:./esp:notasize", ""} {
		var q partFlags
		if err := q.Set(bad); err == nil {
			t.Errorf("Set(%q) should have failed", bad)
		}
	}
}

func TestRoleMapping(t *testing.T) {
	// Roles are the CLI's own vocabulary; the mapping is what makes a disk
	// bootable, so a silent fallback to "data" for esp would be expensive.
	if roleGUID("esp") == roleGUID("data") {
		t.Error("esp and data must not share a partition type")
	}
	if roleGUID("efi") != roleGUID("esp") {
		t.Error("efi is a synonym for esp")
	}
	if roleGUID("root") == roleGUID("data") {
		t.Error("root and data must not share a partition type")
	}
	if roleGUID("nonesuch") != roleGUID("data") {
		t.Error("an unknown role should fall back to data")
	}
	if roleMBRType("esp") == roleMBRType("root") {
		t.Error("esp and root must not share an MBR type")
	}
}

// TestMkfsBuilds runs the command end to end for a content-sized engine and a
// fixed-size one, since only the latter needs -size.
func TestMkfsBuilds(t *testing.T) {
	src := sampleDir(t)
	for _, tc := range []struct {
		fstype string
		size   string
	}{
		{"squashfs", ""},
		{"cpio", ""},
		{"erofs", ""},
		{"ext4", "16M"},
	} {
		t.Run(tc.fstype, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "image."+tc.fstype)
			args := []string{"-type", tc.fstype, "-source", src, "-output", out, "-reproducible"}
			if tc.size != "" {
				args = append(args, "-size", tc.size)
			}
			var code int
			quiet(t, func() { code = run(append([]string{"mkfs"}, args...)) })
			if code != 0 {
				t.Fatalf("mkfs %s exited %d", tc.fstype, code)
			}
			st, err := os.Stat(out)
			if err != nil {
				t.Fatal(err)
			}
			if st.Size() == 0 {
				t.Error("wrote an empty image")
			}
		})
	}
}

// TestMkfsReportsEngineErrors checks a library failure reaches the exit status
// rather than being swallowed into a success.
func TestMkfsReportsEngineErrors(t *testing.T) {
	src := sampleDir(t)
	cases := map[string][]string{
		"unknown type": {"-type", "nosuchfs", "-source", src, "-output", filepath.Join(t.TempDir(), "x")},
		"missing size": {"-type", "ext4", "-source", src, "-output", filepath.Join(t.TempDir(), "x")},
		"missing source": {"-type", "squashfs", "-source", filepath.Join(t.TempDir(), "absent"),
			"-output", filepath.Join(t.TempDir(), "x")},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var code int
			out := quiet(t, func() { code = run(append([]string{"mkfs"}, args...)) })
			if code != 1 {
				t.Errorf("exited %d, want 1", code)
			}
			if !strings.Contains(out, "fsforge:") {
				t.Errorf("the error should be reported on stderr, got %q", out)
			}
		})
	}
}

func TestConvertRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	img := filepath.Join(tmp, "root.sqsh")
	back := filepath.Join(tmp, "back")

	var code int
	quiet(t, func() {
		code = run([]string{"convert", "-from", "dir:" + sampleDir(t), "-to", "squashfs:" + img, "-reproducible"})
	})
	if code != 0 {
		t.Fatalf("convert to squashfs exited %d", code)
	}
	quiet(t, func() {
		code = run([]string{"convert", "-from", "squashfs:" + img, "-to", "dir:" + back})
	})
	if code != 0 {
		t.Fatalf("convert back to a directory exited %d", code)
	}
	got, err := os.ReadFile(filepath.Join(back, "etc", "hosts"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "127.0.0.1 localhost\n" {
		t.Errorf("round-tripped contents = %q", got)
	}
}

func TestConvertRejectsBadLocations(t *testing.T) {
	for _, args := range [][]string{
		{"convert", "-from", "nocolon", "-to", "dir:/tmp/x"},
		{"convert", "-from", "dir:/tmp/x", "-to", "nocolon"},
		{"convert", "-from", "nosuchkind:/tmp/x", "-to", "dir:" + t.TempDir()},
	} {
		var code int
		quiet(t, func() { code = run(args) })
		if code != 1 {
			t.Errorf("run(%q) = %d, want 1", args, code)
		}
	}
}

func TestDiskBuilds(t *testing.T) {
	src := sampleDir(t)
	for _, scheme := range []string{"gpt", "mbr"} {
		t.Run(scheme, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "disk.img")
			var code int
			quiet(t, func() {
				code = run([]string{"disk", "-output", out, "-size", "48M", "-scheme", scheme,
					"-part", "esp:fat:" + src + ":16M", "-part", "root:ext4:" + src + ":rest",
					"-reproducible"})
			})
			if code != 0 {
				t.Fatalf("disk exited %d", code)
			}
			st, err := os.Stat(out)
			if err != nil {
				t.Fatal(err)
			}
			if st.Size() != 48<<20 {
				t.Errorf("disk is %d bytes, want %d", st.Size(), 48<<20)
			}
		})
	}
}

// TestDiskQcow2 covers the container branch, which takes a different path
// through the writer and finalises explicitly.
func TestDiskQcow2(t *testing.T) {
	out := filepath.Join(t.TempDir(), "disk.qcow2")
	var code int
	quiet(t, func() {
		code = run([]string{"disk", "-output", out, "-size", "32M",
			"-part", "root:ext4:" + sampleDir(t) + ":rest", "-reproducible"})
	})
	if code != 0 {
		t.Fatalf("qcow2 disk exited %d", code)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 4 || string(b[:4]) != "QFI\xfb" {
		t.Errorf("output does not start with the QCOW2 magic: % x", b[:min(4, len(b))])
	}
	// A container of a 32 MiB disk holding a few files must be far smaller than
	// the disk, or it is not sparse.
	if int64(len(b)) > 24<<20 {
		t.Errorf("qcow2 is %d bytes for a 32 MiB disk; sparseness is gone", len(b))
	}
}

func TestDiskRejectsBadInput(t *testing.T) {
	src := sampleDir(t)
	cases := map[string][]string{
		"unknown scheme": {"-output", filepath.Join(t.TempDir(), "d.img"), "-size", "32M",
			"-scheme", "apm", "-part", "root:ext4:" + src + ":rest"},
		"bad size": {"-output", filepath.Join(t.TempDir(), "d.img"), "-size", "notasize",
			"-part", "root:ext4:" + src + ":rest"},
		"unknown fstype": {"-output", filepath.Join(t.TempDir(), "d.img"), "-size", "32M",
			"-part", "root:nosuchfs:" + src + ":rest"},
		"partitions exceed disk": {"-output", filepath.Join(t.TempDir(), "d.img"), "-size", "8M",
			"-part", "root:ext4:" + src + ":64M"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var code int
			quiet(t, func() { code = run(append([]string{"disk"}, args...)) })
			if code != 1 {
				t.Errorf("exited %d, want 1", code)
			}
		})
	}
}

func TestOCIAddLayer(t *testing.T) {
	tmp := t.TempDir()
	layout := filepath.Join(tmp, "image-oci")
	src := sampleDir(t)

	var code int
	quiet(t, func() {
		code = run([]string{"convert", "-from", "dir:" + src, "-to", "oci:" + layout,
			"-ref", "app:v1", "-reproducible"})
	})
	if code != 0 {
		t.Fatalf("building the layout exited %d", code)
	}

	patch := t.TempDir()
	if err := os.WriteFile(filepath.Join(patch, "added"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A bare directory is accepted as well as <kind>:<path>, which is the branch
	// worth covering: the argument has no colon to split on.
	quiet(t, func() {
		code = run([]string{"oci-add-layer", "-image", layout, "-ref", "app:v1",
			"-from", patch, "-reproducible"})
	})
	if code != 0 {
		t.Fatalf("oci-add-layer with a bare directory exited %d", code)
	}
	quiet(t, func() {
		code = run([]string{"oci-add-layer", "-image", layout, "-ref", "app:v1",
			"-from", "dir:" + patch, "-diff", "-reproducible"})
	})
	if code != 0 {
		t.Fatalf("oci-add-layer with a kind:path source exited %d", code)
	}

	// Flattening the result must show the added file, which is what proves the
	// layer went on rather than merely being written somewhere.
	back := filepath.Join(tmp, "flat")
	quiet(t, func() {
		code = run([]string{"convert", "-from", "oci:" + layout, "-to", "dir:" + back})
	})
	if code != 0 {
		t.Fatalf("flattening exited %d", code)
	}
	if _, err := os.Stat(filepath.Join(back, "added")); err != nil {
		t.Errorf("the added layer is not in the flattened image: %v", err)
	}
}

func TestOCIAddLayerRejectsBadInput(t *testing.T) {
	cases := map[string][]string{
		"missing layout": {"-from", t.TempDir()},
		"absent layout":  {"-image", filepath.Join(t.TempDir(), "nope"), "-from", t.TempDir()},
		"bad source kind": {"-image", filepath.Join(t.TempDir(), "nope"),
			"-from", "nosuchkind:/tmp/x"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var code int
			quiet(t, func() { code = run(append([]string{"oci-add-layer"}, args...)) })
			if code != 1 {
				t.Errorf("exited %d, want 1", code)
			}
		})
	}
}

// TestReproducibleFlag checks the flag reaches the engines: two builds of the
// same tree must be identical, which is the CLI's headline promise.
func TestReproducibleFlag(t *testing.T) {
	src := sampleDir(t)
	build := func() []byte {
		out := filepath.Join(t.TempDir(), "image.sqsh")
		var code int
		quiet(t, func() {
			code = run([]string{"mkfs", "-type", "squashfs", "-source", src,
				"-output", out, "-reproducible"})
		})
		if code != 0 {
			t.Fatalf("mkfs exited %d", code)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if a, b := build(), build(); string(a) != string(b) {
		t.Errorf("two reproducible builds differ (%d vs %d bytes)", len(a), len(b))
	}
}

// TestSpecCommandAndFlag covers the pair that makes the workflow: describe a
// directory, edit the description, build with it. They are tested together
// because neither is much use alone.
func TestSpecCommandAndFlag(t *testing.T) {
	src := sampleDir(t)
	spec := filepath.Join(t.TempDir(), "tree.mtree")

	var code int
	quiet(t, func() { code = run([]string{"spec", "-source", src, "-output", spec}) })
	if code != 0 {
		t.Fatalf("spec exited %d", code)
	}
	described, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(described), "./etc/hosts") {
		t.Errorf("the generated spec does not describe the tree:\n%s", described)
	}

	// The edit a real user makes: own everything as root and add a device node.
	edited := string(described) + "./dev type=dir mode=0755 uid=0 gid=0\n" +
		"./dev/console type=char mode=0600 uid=0 gid=0 device=native,5,1\n"
	if err := os.WriteFile(spec, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "root.img")
	quiet(t, func() {
		code = run([]string{"mkfs", "-type", "ext4", "-source", src, "-output", out,
			"-size", "16M", "-spec", spec, "-reproducible"})
	})
	if code != 0 {
		t.Fatalf("mkfs -spec exited %d", code)
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		t.Fatalf("no image was written: %v", err)
	}

	// spec with no -source, and mkfs pointing at a spec that is not there.
	quiet(t, func() { code = run([]string{"spec"}) })
	if code != 1 {
		t.Errorf("spec without -source exited %d, want 1", code)
	}
	quiet(t, func() {
		code = run([]string{"mkfs", "-type", "squashfs", "-source", src,
			"-output", filepath.Join(t.TempDir(), "x"), "-spec", "/nonexistent.mtree"})
	})
	if code != 1 {
		t.Errorf("mkfs with a missing spec exited %d, want 1", code)
	}
}

// TestSpecToStdout covers the default output path, which is what makes the
// command pipeable.
func TestSpecToStdout(t *testing.T) {
	src := sampleDir(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	code := run([]string{"spec", "-source", src})
	os.Stdout = old
	w.Close()
	got := <-done

	if code != 0 {
		t.Fatalf("spec exited %d", code)
	}
	if !strings.HasPrefix(got, "#mtree") {
		t.Errorf("stdout does not start with the mtree marker:\n%s", got)
	}
}

// TestVersion covers the command a bug report starts with. The value is stamped
// at link time or read from the module, so what is checked here is that it
// prints something identifying rather than what that something is.
func TestVersion(t *testing.T) {
	for _, arg := range []string{"version", "-version", "--version"} {
		var code int
		out := captureStdout(t, func() { code = run([]string{arg}) })
		if code != 0 {
			t.Errorf("run(%q) = %d, want 0", arg, code)
		}
		if !strings.HasPrefix(out, "fsforge ") || !strings.Contains(out, runtime.GOARCH) {
			t.Errorf("run(%q) printed %q", arg, out)
		}
	}
}

// captureStdout runs f with stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	f()
	os.Stdout = old
	w.Close()
	return <-done
}
