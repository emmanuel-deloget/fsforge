package mtree

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func parse(t *testing.T, s string) *Spec {
	t.Helper()
	spec, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return spec
}

func byPath(spec *Spec) map[string]Entry {
	out := map[string]Entry{}
	for _, e := range spec.Entries {
		out[e.Path] = e
	}
	return out
}

// TestParseFullPathDialect covers the 2.0 form libarchive and bsdtar emit.
func TestParseFullPathDialect(t *testing.T) {
	spec := parse(t, `#mtree
./etc type=dir mode=0755 uid=0 gid=0
./etc/hosts type=file mode=0644 uid=0 gid=0 size=20
./dev/console type=char mode=0600 device=native,5,1
./bin/ping type=file mode=4755 uid=0
./lib/libc.so type=link link=libc.so.6
`)
	e := byPath(spec)
	if len(e) != 5 {
		t.Fatalf("got %d entries, want 5: %v", len(e), e)
	}
	if got := e["etc/hosts"]; got.Mode == nil || *got.Mode != 0o644 || got.Size == nil || *got.Size != 20 {
		t.Errorf("etc/hosts parsed as %+v", got)
	}
	if got := e["dev/console"]; got.Major == nil || *got.Major != 5 || *got.Minor != 1 {
		t.Errorf("device numbers lost: %+v", got)
	}
	if got := e["bin/ping"]; got.Mode == nil || *got.Mode&fs.ModeSetuid == 0 {
		t.Errorf("setuid bit lost: %v", got.Mode)
	}
	if got := e["lib/libc.so"]; got.Link == nil || *got.Link != "libc.so.6" {
		t.Errorf("link target lost: %+v", got)
	}
}

// TestParseHierarchicalDialect covers the classic form, where a bare name hangs
// off the directory the previous lines opened and ".." closes it.
func TestParseHierarchicalDialect(t *testing.T) {
	spec := parse(t, `#mtree
/set type=file uid=0 gid=0 mode=0644
.           type=dir mode=0755
etc         type=dir
hosts       size=20
resolv.conf size=30
..
usr         type=dir
bin         type=dir
sh          mode=0755
..
..
top         size=1
`)
	e := byPath(spec)
	want := []string{"", "etc", "etc/hosts", "etc/resolv.conf", "usr", "usr/bin", "usr/bin/sh", "top"}
	for _, p := range want {
		if _, ok := e[p]; !ok {
			t.Errorf("missing entry %q; got %v", p, keysOf(e))
		}
	}
	if len(e) != len(want) {
		t.Errorf("got %d entries, want %d: %v", len(e), len(want), keysOf(e))
	}
	// /set defaults must reach entries that do not restate them.
	if got := e["etc/hosts"]; got.UID == nil || *got.UID != 0 || got.Mode == nil || *got.Mode != 0o644 {
		t.Errorf("defaults did not reach etc/hosts: %+v", got)
	}
	// ".." must close a level, so "top" is back at the root.
	if _, ok := e["top"]; !ok {
		t.Error("\"..\" did not return to the root")
	}
}

func TestParseSetAndUnset(t *testing.T) {
	spec := parse(t, `/set type=file uid=7 gid=8
./a
/unset uid
./b
/set gid=9
./c
`)
	e := byPath(spec)
	if got := e["a"]; got.UID == nil || *got.UID != 7 {
		t.Errorf("a should inherit uid 7: %+v", got)
	}
	if got := e["b"]; got.UID != nil {
		t.Errorf("b should have no uid after /unset: %v", *got.UID)
	}
	if got := e["c"]; got.GID == nil || *got.GID != 9 {
		t.Errorf("c should see the newer gid: %+v", got)
	}
}

// TestParseEscapes covers names a shell would mangle, which is the reason the
// format has escaping at all.
func TestParseEscapes(t *testing.T) {
	spec := parse(t, `./with\040space type=file
./tab\011here type=file
./back\\slash type=file
`)
	e := byPath(spec)
	for _, want := range []string{"with space", "tab\there", `back\slash`} {
		if _, ok := e[want]; !ok {
			t.Errorf("missing %q; got %v", want, keysOf(e))
		}
	}
}

func TestParseIgnoresUnknownKeywords(t *testing.T) {
	// A spec written for another tool carries checksums and flags; refusing them
	// would make interoperability the exception.
	spec := parse(t, "./a type=file sha256digest=deadbeef flags=none cksum=1234 mode=0600\n")
	e := byPath(spec)["a"]
	if e.Mode == nil || *e.Mode != 0o600 {
		t.Errorf("known keywords should survive unknown ones: %+v", e)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"unknown type":       "./a type=nonesuch\n",
		"bad mode":           "./a mode=99999999999\n",
		"bad uid":            "./a uid=notanumber\n",
		"bad size":           "./a size=x\n",
		"bad device":         "./a type=char device=native,5\n",
		"bad time":           "./a time=x.y\n",
		"/set without value": "/set type\n",
		"dangling backslash": "./a\\\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(src)); err == nil {
				t.Error("should have been refused")
			} else if !strings.Contains(err.Error(), "line ") {
				t.Errorf("the error should name a line: %v", err)
			}
		})
	}
}

// TestApplyAmendsWithoutClobbering is the property that makes a spec usable
// over a populated tree: keywords that are absent leave the node alone.
func TestApplyAmendsWithoutClobbering(t *testing.T) {
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	file := &image.Node{
		Inode: tree.Inode{
			Meta:    tree.Meta{Mode: 0o600, UID: 1000, GID: 1000, ModTime: time.Unix(42, 0).UTC()},
			Content: tree.Bytes("payload"),
		},
		Nlink: 1,
	}
	if err := root.AddChild("file", file); err != nil {
		t.Fatal(err)
	}

	c, err := Apply(root, parse(t, "./file uid=0 gid=0\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if file.UID != 0 || file.GID != 0 {
		t.Errorf("owner not applied: %d:%d", file.UID, file.GID)
	}
	if file.Mode != 0o600 {
		t.Errorf("mode should have been left alone, got %v", file.Mode)
	}
	if file.ModTime != time.Unix(42, 0).UTC() {
		t.Errorf("time should have been left alone, got %v", file.ModTime)
	}
	if string(readAll(t, file.Content)) != "payload" {
		t.Error("contents should have been left alone")
	}
}

// TestApplyCreatesWhatACheckoutCannotHold is the reason the package exists.
func TestApplyCreatesWhatACheckoutCannotHold(t *testing.T) {
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	c, err := Apply(root, parse(t, `./dev type=dir mode=0755 uid=0 gid=0
./dev/console type=char mode=0600 uid=0 gid=0 device=native,5,1
./dev/null type=char mode=0666 device=native,1,3
./run/lock type=dir mode=01777
./var/run type=link link=../run
./pipe type=fifo mode=0600
`))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	dev := findChild(root, "dev")
	if dev == nil || !dev.IsDir() {
		t.Fatal("dev was not created")
	}
	console := findChild(dev, "console")
	if console == nil || console.Mode&fs.ModeCharDevice == 0 {
		t.Fatalf("console is not a character device: %v", console)
	}
	if console.Rdev != 5<<8|1 {
		t.Errorf("console rdev = %#x, want %#x", console.Rdev, 5<<8|1)
	}
	// An intermediate directory the spec never names must still appear.
	run := findChild(root, "run")
	if run == nil || !run.IsDir() {
		t.Fatal("run/ was not created for run/lock")
	}
	if lock := findChild(run, "lock"); lock == nil || lock.Mode&fs.ModeSticky == 0 {
		t.Errorf("sticky bit lost on run/lock: %v", lock)
	}
	if l := findChild(findChild(root, "var"), "run"); l == nil || l.Link != "../run" {
		t.Errorf("symlink not created: %v", l)
	}
	if p := findChild(root, "pipe"); p == nil || p.Mode&fs.ModeNamedPipe == 0 {
		t.Errorf("fifo not created: %v", p)
	}
}

func TestApplyContents(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(src, []byte("from the host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	c, err := Apply(root, parse(t, "./etc/motd type=file mode=0644 contents="+vis(src)+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	motd := findChild(findChild(root, "etc"), "motd")
	if motd == nil {
		t.Fatal("etc/motd was not created")
	}
	if got := string(readAll(t, motd.Content)); got != "from the host\n" {
		t.Errorf("contents = %q", got)
	}

	// A contents= naming a file that is not there must be reported, not ignored.
	if _, err := Apply(root, parse(t, "./x contents=/nonexistent/path\n")); err == nil {
		t.Error("a missing contents file should be an error")
	}
}

func TestApplyRejectsEscapingPaths(t *testing.T) {
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	// path.Clean folds "../" at the root away, so the danger is a name that
	// survives cleaning; either way nothing may leave the tree.
	for _, src := range []string{"./a/../../etc/passwd type=file\n", "./a/./../../x type=file\n"} {
		if _, err := Apply(root, parse(t, src)); err == nil {
			if findChild(root, "..") != nil {
				t.Errorf("%q put a %q entry in the tree", src, "..")
			}
		}
	}
	if findChild(root, "..") != nil || findChild(root, ".") != nil {
		t.Error("dot entries reached the tree")
	}
}

// TestWriteReadRoundTrip checks a written spec parses back to the same facts,
// which is what makes "generate, edit, apply" a workflow rather than a hope.
func TestWriteReadRoundTrip(t *testing.T) {
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	c, err := Apply(root, parse(t, `./etc type=dir mode=0755 uid=0 gid=0
./etc/hosts type=file mode=0644 uid=0 gid=0
./etc/with\040space type=file mode=0600
./dev/console type=char mode=0600 device=native,5,1
./link type=link link=etc/hosts
./setuid type=file mode=4755
`))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var buf bytes.Buffer
	if err := Write(&buf, root); err != nil {
		t.Fatal(err)
	}
	again := parse(t, buf.String())
	e := byPath(again)

	if got := e["dev/console"]; got.Major == nil || *got.Major != 5 || *got.Minor != 1 {
		t.Errorf("device numbers did not survive: %+v", got)
	}
	if got := e["etc/with space"]; got.Mode == nil || *got.Mode != 0o600 {
		t.Errorf("a name with a space did not survive: %v", keysOf(e))
	}
	if got := e["link"]; got.Link == nil || *got.Link != "etc/hosts" {
		t.Errorf("link target did not survive: %+v", got)
	}
	if got := e["setuid"]; got.Mode == nil || *got.Mode&fs.ModeSetuid == 0 {
		t.Errorf("setuid bit did not survive: %v", got.Mode)
	}
	// Writing twice must give the same bytes, or a spec cannot be diffed.
	var again2 bytes.Buffer
	if err := Write(&again2, root); err != nil {
		t.Fatal(err)
	}
	if buf.String() != again2.String() {
		t.Error("two writes of one tree differ")
	}
}

func TestFromDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "etc", "hosts"), []byte("127.0.0.1\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("etc/hosts", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var buf bytes.Buffer
	if err := FromDir(&buf, dir); err != nil {
		t.Fatal(err)
	}
	e := byPath(parse(t, buf.String()))
	if got := e["etc/hosts"]; got.Mode == nil || *got.Mode != 0o640 {
		t.Errorf("mode not described: %+v", got)
	}
	if got := e["etc/hosts"]; got.Size == nil || *got.Size != 10 {
		t.Errorf("size not described: %+v", got)
	}
	if got := e["link"]; got.Link == nil || *got.Link != "etc/hosts" {
		t.Errorf("symlink not described: %+v", got)
	}
	if err := FromDir(&buf, filepath.Join(dir, "absent")); err == nil {
		t.Error("describing a directory that is not there should fail")
	}
}

func keysOf(m map[string]Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func readAll(t *testing.T, s tree.Source) []byte {
	t.Helper()
	if s == nil {
		return nil
	}
	b := make([]byte, s.Size())
	if len(b) == 0 {
		return nil
	}
	if _, err := s.ReadAt(b, 0); err != nil {
		t.Fatal(err)
	}
	return b
}

// TestApplyKeepsDirectoryLinkCounts pins the accounting a filesystem check
// looks at: every subdirectory's ".." counts as a link to its parent. Getting
// it wrong produced a tree e2fsck refused — "ref count is 4, should be 7" —
// while every unit test here still passed, which is why this one exists.
func TestApplyKeepsDirectoryLinkCounts(t *testing.T) {
	root := &image.Node{Inode: tree.Inode{Meta: tree.Meta{Mode: fs.ModeDir | 0o755}}, Nlink: 2}
	c, err := Apply(root, parse(t, `./bin type=dir
./dev type=dir
./dev/pts type=dir
./tmp type=dir
./var/run/lock type=dir
./file type=file
`))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// bin, dev, tmp, var: four subdirectories, plus the root's own two.
	if root.Nlink != 6 {
		t.Errorf("root nlink = %d, want 6 (2 + four subdirectories)", root.Nlink)
	}
	if dev := findChild(root, "dev"); dev == nil || dev.Nlink != 3 {
		t.Errorf("dev nlink = %v, want 3 (2 + dev/pts)", dev)
	}
	// var was never named by the spec, only walked through; it still counts.
	v := findChild(root, "var")
	if v == nil || v.Nlink != 3 {
		t.Errorf("var nlink = %v, want 3 (2 + var/run)", v)
	}
	if pts := findChild(findChild(root, "dev"), "pts"); pts == nil || pts.Nlink != 2 {
		t.Errorf("a leaf directory should be 2, got %v", pts)
	}
	// A file must not have moved its parent's count.
	if f := findChild(root, "file"); f == nil || f.Nlink != 1 {
		t.Errorf("file nlink = %v, want 1", f)
	}
}
