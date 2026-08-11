package manifest

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// Field selects what Diff compares.
type Field uint32

const (
	Type    Field = 1 << iota // node kind: dir, regular, symlink, device, fifo
	Perm                      // permission bits, including setuid/setgid/sticky
	Owner                     // uid and gid
	MTime                     // modification time, at one-second resolution
	Nlink                     // link count
	Size                      // regular file size
	Content                   // sha256 of regular file contents
	Link                      // symlink target
	Rdev                      // device major/minor
	Links                     // hard-link sharing, via LinkGroup
	Xattrs                    // extended attributes
)

// All is every field. A format subtracts what it cannot hold, which documents
// the format's limits in the test that exercises it.
const All = Type | Perm | Owner | MTime | Nlink | Size | Content | Link | Rdev | Links | Xattrs

var fieldNames = []struct {
	f Field
	n string
}{
	{Type, "type"}, {Perm, "perm"}, {Owner, "owner"}, {MTime, "mtime"},
	{Nlink, "nlink"}, {Size, "size"}, {Content, "content"}, {Link, "link"},
	{Rdev, "rdev"}, {Links, "links"}, {Xattrs, "xattrs"},
}

// String renders a field set as a stable, comma-separated list.
func (f Field) String() string {
	var out []string
	for _, fn := range fieldNames {
		if f&fn.f != 0 {
			out = append(out, fn.n)
		}
	}
	if out == nil {
		return "none"
	}
	return strings.Join(out, ",")
}

// Options tunes a comparison to a format's realities.
type Options struct {
	// Fields selects what to compare; zero means All.
	Fields Field
	// MTimeGrain is the coarsest timestamp resolution the format stores. Zero
	// means one second. exFAT and FAT keep DOS timestamps, whose two-second
	// resolution is a property of the format rather than a defect.
	MTimeGrain time.Duration
	// Minted lists paths the format creates by itself, tolerated when they show
	// up only in the readback — ext's lost+found, for instance.
	Minted []string
	// SkipDirMTime leaves directory timestamps out of the comparison. Extraction
	// tools routinely stamp directories with the time of extraction — GNU cpio
	// does — because writing a directory's contents touches it. That is the
	// tool's behaviour, not the image's, so a test that compares against an
	// extraction has to set this or read a false loss on every directory.
	SkipDirMTime bool
}

// Diff compares got against want and returns one line per difference. An empty
// result means the two agree on everything asked for.
//
// want is the reference — the tree that was written — and got is the readback,
// so a missing path reads as a loss rather than an addition.
func Diff(want, got Manifest, o Options) []string {
	if o.Fields == 0 {
		o.Fields = All
	}
	if o.MTimeGrain <= 0 {
		o.MTimeGrain = time.Second
	}
	var out []string
	wi := index(want)
	gi := index(got)
	minted := map[string]bool{}
	for _, p := range o.Minted {
		minted[p] = true
	}

	for _, w := range want {
		g, ok := gi[w.Path]
		if !ok {
			out = append(out, fmt.Sprintf("%s: missing from readback", w.Path))
			continue
		}
		out = append(out, diffEntry(w, g, o)...)
	}
	for _, g := range got {
		if _, ok := wi[g.Path]; !ok && !minted[g.Path] {
			out = append(out, fmt.Sprintf("%s: unexpected in readback (%s)", g.Path, kind(g.Mode)))
		}
	}
	sort.Strings(out)
	return out
}

func index(m Manifest) map[string]Entry {
	out := make(map[string]Entry, len(m))
	for _, e := range m {
		out[e.Path] = e
	}
	return out
}

func diffEntry(w, g Entry, o Options) []string {
	f := o.Fields
	var out []string
	report := func(field, want, got string) {
		out = append(out, fmt.Sprintf("%s: %s want %s, got %s", w.Path, field, want, got))
	}

	if f&Type != 0 && w.Mode&fs.ModeType != g.Mode&fs.ModeType {
		// Two different bit patterns can share a label — ModeCharDevice with and
		// without ModeDevice both read as "chardev" — so show the bits when the
		// names would otherwise make the failure look like nonsense.
		wk, gk := kind(w.Mode), kind(g.Mode)
		if wk == gk {
			wk = fmt.Sprintf("%s (%#o)", wk, uint32(w.Mode&fs.ModeType))
			gk = fmt.Sprintf("%s (%#o)", gk, uint32(g.Mode&fs.ModeType))
		}
		report("type", wk, gk)
	}
	if f&Perm != 0 && permOf(w.Mode) != permOf(g.Mode) {
		report("perm", fmt.Sprintf("%04o", permOf(w.Mode)), fmt.Sprintf("%04o", permOf(g.Mode)))
	}
	if f&Owner != 0 && (w.UID != g.UID || w.GID != g.GID) {
		report("owner", fmt.Sprintf("%d:%d", w.UID, w.GID), fmt.Sprintf("%d:%d", g.UID, g.GID))
	}
	// Compared no more strictly than the format stores: see Options.MTimeGrain.
	// The test is the gap between the two, not a truncation of each — truncating
	// aligns on the epoch, so two instants a second apart can still land in
	// different buckets and read as a loss that never happened.
	if f&MTime != 0 && !(o.SkipDirMTime && w.Mode.IsDir()) &&
		absDur(w.MTime.Sub(g.MTime)) >= o.MTimeGrain {
		report("mtime", w.MTime.UTC().Format(time.RFC3339), g.MTime.UTC().Format(time.RFC3339))
	}
	if f&Nlink != 0 && !w.Mode.IsDir() && w.Nlink != g.Nlink {
		// Directory link counts are an engine's business (". " and ".." inflate
		// them differently per format), so they are out of scope here.
		report("nlink", fmt.Sprint(w.Nlink), fmt.Sprint(g.Nlink))
	}
	if f&Size != 0 && w.Mode.IsRegular() && w.Size != g.Size {
		report("size", fmt.Sprint(w.Size), fmt.Sprint(g.Size))
	}
	if f&Content != 0 && w.Mode.IsRegular() && w.Digest != g.Digest {
		report("content", short(w.Digest), short(g.Digest))
	}
	if f&Link != 0 && w.Mode&fs.ModeSymlink != 0 && w.Link != g.Link {
		report("link", w.Link, g.Link)
	}
	if f&Rdev != 0 && isDevice(w.Mode) && w.Rdev != g.Rdev {
		report("rdev", fmt.Sprintf("%#x", w.Rdev), fmt.Sprintf("%#x", g.Rdev))
	}
	if f&Links != 0 && w.LinkGroup != g.LinkGroup {
		report("links", linkDesc(w.LinkGroup), linkDesc(g.LinkGroup))
	}
	if f&Xattrs != 0 {
		out = append(out, diffXattrs(w, g)...)
	}
	return out
}

func diffXattrs(w, g Entry) []string {
	var out []string
	for _, k := range sortedKeys(w.Xattrs, g.Xattrs) {
		wv, wok := w.Xattrs[k]
		gv, gok := g.Xattrs[k]
		switch {
		case wok && !gok:
			out = append(out, fmt.Sprintf("%s: xattr %s dropped", w.Path, k))
		case !wok && gok:
			out = append(out, fmt.Sprintf("%s: xattr %s added", w.Path, k))
		case !bytes.Equal(wv, gv):
			out = append(out, fmt.Sprintf("%s: xattr %s want %q, got %q", w.Path, k, wv, gv))
		}
	}
	return out
}

func sortedKeys(a, b map[string][]byte) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// permOf keeps the permission bits plus setuid/setgid/sticky, which live
// outside fs.FileMode's low nine bits but are permission bits on disk.
func permOf(m fs.FileMode) fs.FileMode {
	return m.Perm() | (m & (fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky))
}

func isDevice(m fs.FileMode) bool { return m&(fs.ModeDevice|fs.ModeCharDevice) != 0 }

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func kind(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "dir"
	case m&fs.ModeSymlink != 0:
		return "symlink"
	case m&fs.ModeCharDevice != 0:
		return "chardev"
	case m&fs.ModeDevice != 0:
		return "blockdev"
	case m&fs.ModeNamedPipe != 0:
		return "fifo"
	case m&fs.ModeSocket != 0:
		return "socket"
	default:
		return "file"
	}
}

func linkDesc(g int) string {
	if g == 0 {
		return "unshared"
	}
	return fmt.Sprintf("group %d", g)
}

func short(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	if d == "" {
		return "<none>"
	}
	return d
}
