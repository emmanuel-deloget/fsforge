package fsgen

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/emmanuel-deloget/fsforge/internal/manifest"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

func deps() image.Deps {
	return image.Deps{Clock: image.FixedClock{T: time.Unix(1_600_000_000, 0).UTC()}}
}

func generate(t *testing.T, o Options) manifest.Manifest {
	t.Helper()
	mem, err := Generate(deps(), o)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	m, err := manifest.FromTree(mem.RootNode())
	if err != nil {
		t.Fatalf("FromTree: %v", err)
	}
	return m
}

func full() Caps {
	return Caps{Symlinks: true, Devices: true, HardLinks: true,
		NonUTF8: true, Owners: true, SpecMode: true, Times: true}
}

// TestDeterministic is the property the whole differential suite rests on: a
// failure is only reproducible from its seed if the same seed rebuilds the same
// tree, byte for byte.
func TestDeterministic(t *testing.T) {
	a := generate(t, Options{Seed: 7, Caps: full()})
	b := generate(t, Options{Seed: 7, Caps: full()})
	if d := manifest.Diff(a, b, manifest.Options{}); len(d) != 0 {
		t.Errorf("same seed produced different trees:\n%s", strings.Join(d, "\n"))
	}
	if c := generate(t, Options{Seed: 8, Caps: full()}); len(manifest.Diff(a, c, manifest.Options{})) == 0 {
		t.Error("two seeds produced identical trees, so the seed does nothing")
	}
}

// TestCapsAreRespected is what makes a passing differential test mean anything:
// when a format declares it cannot hold symlinks, the absence of a complaint
// must come from the format keeping what it was given, not from the generator
// quietly emitting nothing to lose.
func TestCapsAreRespected(t *testing.T) {
	m := generate(t, Options{Seed: 3, Files: 60, Caps: Caps{}})
	for _, e := range m {
		switch {
		case e.Mode&fs.ModeSymlink != 0:
			t.Errorf("%s: symlink emitted while Caps.Symlinks is false", e.Path)
		case e.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
			t.Errorf("%s: special file emitted while Caps.Devices is false", e.Path)
		}
		if e.LinkGroup != 0 {
			t.Errorf("%s: hard link emitted while Caps.HardLinks is false", e.Path)
		}
		if e.UID != 0 || e.GID != 0 {
			t.Errorf("%s: owner %d:%d set while Caps.Owners is false", e.Path, e.UID, e.GID)
		}
		if e.Mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
			t.Errorf("%s: special mode bit set while Caps.SpecMode is false", e.Path)
		}
		if base := path(e.Path); !utf8.ValidString(base) {
			t.Errorf("%s: non-UTF-8 name emitted while Caps.NonUTF8 is false", e.Path)
		}
	}
}

// TestCapsProduceWhatTheyEnable is the other half: a capability that is on must
// actually show up, or the test that relies on it proves nothing either.
func TestCapsProduceWhatTheyEnable(t *testing.T) {
	m := generate(t, Options{Seed: 3, Files: 120, Caps: full()})
	var symlinks, specials, shared, owned, special, nonUTF8 int
	for _, e := range m {
		switch {
		case e.Mode&fs.ModeSymlink != 0:
			symlinks++
		case e.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
			specials++
		}
		if e.LinkGroup != 0 {
			shared++
		}
		if e.UID != 0 || e.GID != 0 {
			owned++
		}
		if e.Mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
			special++
		}
		if !utf8.ValidString(path(e.Path)) {
			nonUTF8++
		}
	}
	for _, c := range []struct {
		n    int
		what string
	}{
		{symlinks, "symlinks"}, {specials, "special files"}, {shared, "hard links"},
		{owned, "non-zero owners"}, {special, "setuid/setgid/sticky bits"},
		{nonUTF8, "non-UTF-8 names"},
	} {
		if c.n == 0 {
			t.Errorf("no %s were generated even though the capability is on", c.what)
		}
	}
}

func TestLimitsAreRespected(t *testing.T) {
	const maxName, maxDepth, maxLink = 32, 3, 12
	m := generate(t, Options{Seed: 5, Files: 80, Caps: Caps{
		MaxName: maxName, MaxDepth: maxDepth, MaxLink: maxLink, Symlinks: true,
	}})
	for _, e := range m {
		if n := path(e.Path); len(n) > maxName {
			t.Errorf("%s: name is %d bytes, over the %d cap", e.Path, len(n), maxName)
		}
		// MaxDepth bounds directory nesting, so a leaf may sit one level deeper.
		if d := strings.Count(e.Path, "/"); d > maxDepth+1 {
			t.Errorf("%s: nested %d deep, over the %d cap", e.Path, d, maxDepth)
		}
		if e.Mode&fs.ModeSymlink != 0 && len(e.Link) > maxLink {
			t.Errorf("%s: target is %d bytes, over the %d cap", e.Path, len(e.Link), maxLink)
		}
	}
}

func TestFileCountIsHonoured(t *testing.T) {
	for _, want := range []int{5, 25, 100} {
		m := generate(t, Options{Seed: 11, Files: want, Caps: full()})
		var leaves int
		for _, e := range m {
			if !e.Mode.IsDir() && e.LinkGroup == 0 {
				leaves++
			}
		}
		// Hard links add names beyond the budget, so this is a floor plus slack.
		if leaves < want-3 {
			t.Errorf("asked for ~%d files, got %d leaves", want, leaves)
		}
	}
}

func TestContentSpansTheInterestingSizes(t *testing.T) {
	m := generate(t, Options{Seed: 13, Files: 150, Caps: Caps{}})
	var empty, sub, block, multi bool
	for _, e := range m {
		if !e.Mode.IsRegular() {
			continue
		}
		switch {
		case e.Size == 0:
			empty = true
		case e.Size < 4096:
			sub = true
		case e.Size == 4096:
			block = true
		case e.Size > 4096:
			multi = true
		}
	}
	if !empty || !sub || !block || !multi {
		t.Errorf("sizes missing: empty=%v sub-block=%v one-block=%v multi-block=%v",
			empty, sub, block, multi)
	}
}

func TestGenerateRejectsImpossibleNames(t *testing.T) {
	// One byte of room cannot hold a unique name for long; the generator should
	// say so rather than loop or collide.
	_, err := Generate(deps(), Options{Seed: 1, Files: 50, Caps: Caps{MaxName: 1}})
	if err == nil {
		t.Fatal("a one-byte name cap should be reported, not worked around")
	}
	if !strings.Contains(err.Error(), "unique name") {
		t.Errorf("error should say what went wrong, got %v", err)
	}
}

func TestWriteDir(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "tree")
	mem, err := Generate(deps(), Options{Seed: 17, Files: 40, Caps: Caps{
		Symlinks: true, Devices: true, NonUTF8: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := WriteDir(mem.RootNode(), dst)
	if err != nil {
		t.Fatalf("WriteDir: %v", err)
	}
	want, err := manifest.FromTree(mem.RootNode())
	if err != nil {
		t.Fatal(err)
	}

	// Special files are skipped on the way out — they need privileges — so the
	// host tree is compared on what it could carry.
	var expected manifest.Manifest
	for _, e := range want {
		if e.Mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) == 0 {
			expected = append(expected, e)
		}
	}
	d := manifest.Diff(expected, got, manifest.Options{
		Fields: manifest.Type | manifest.Perm | manifest.Size | manifest.Content | manifest.Link,
	})
	if len(d) != 0 {
		t.Errorf("host tree differs from the generated one:\n%s", strings.Join(d, "\n"))
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("destination missing: %v", err)
	}
}

// path returns the last component of a slash-separated manifest path.
func path(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
