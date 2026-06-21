package oci

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io/fs"
	"testing"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// tarEntry describes one header (plus optional body) for a synthetic layer.
type tarEntry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
	link     string
	major    int64
	minor    int64
	xattr    map[string]string
}

func makeLayer(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     e.mode,
			Size:     int64(len(e.body)),
			Linkname: e.link,
			Devmajor: e.major,
			Devminor: e.minor,
			ModTime:  time.Unix(1_700_000_000, 0),
		}
		if e.xattr != nil {
			hdr.Format = tar.FormatPAX
			hdr.PAXRecords = map[string]string{}
			for k, v := range e.xattr {
				hdr.PAXRecords["SCHILY.xattr."+k] = v
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestFlattenLayersWhiteoutsDevices builds a two-layer layout by hand and
// flattens it, covering whiteouts (regular + opaque), device/fifo nodes, hard
// links, xattrs and setuid bits — paths Build alone does not reach.
func TestFlattenLayersWhiteoutsDevices(t *testing.T) {
	dir := t.TempDir()
	l, err := CreateLayout(dir)
	if err != nil {
		t.Fatal(err)
	}

	layer1 := makeLayer(t, []tarEntry{
		{name: "etc/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "etc/hosts", typeflag: tar.TypeReg, mode: 0o4644, body: "h",
			xattr: map[string]string{"user.foo": "bar"}},
		{name: "etc/old", typeflag: tar.TypeReg, mode: 0o644, body: "old"},
		{name: "bin/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "bin/app", typeflag: tar.TypeReg, mode: 0o755, body: "app"},
		{name: "bin/applink", typeflag: tar.TypeLink, link: "bin/app"},
		{name: "dev/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "dev/null", typeflag: tar.TypeChar, mode: 0o666, major: 1, minor: 3},
		{name: "dev/sda", typeflag: tar.TypeBlock, mode: 0o660, major: 8, minor: 0},
		{name: "run.pipe", typeflag: tar.TypeFifo, mode: 0o644},
		{name: "sym", typeflag: tar.TypeSymlink, link: "etc/hosts"},
		{name: "opq/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "opq/keep", typeflag: tar.TypeReg, mode: 0o644, body: "k"},
	})
	layer2 := makeLayer(t, []tarEntry{
		{name: "etc/.wh.old", typeflag: tar.TypeReg},      // delete etc/old
		{name: "opq/.wh..wh..opq", typeflag: tar.TypeReg}, // clear opq/*
		{name: "bin/app", typeflag: tar.TypeReg, mode: 0o755, body: "app2"}, // overwrite
	})

	d1, err := l.PutBlobBytes(MediaTypeLayerTar, layer1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := l.PutBlobBytes(MediaTypeLayerTar, layer2)
	if err != nil {
		t.Fatal(err)
	}

	cfg := Image{
		Architecture: "amd64", OS: "linux",
		RootFS: RootFS{Type: "layers", DiffIDs: []string{"sha256:aa", "sha256:bb"}},
	}
	cfgJSON, _ := json.Marshal(cfg)
	cfgDesc, err := l.PutBlobBytes(MediaTypeConfig, cfgJSON)
	if err != nil {
		t.Fatal(err)
	}
	man := Manifest{SchemaVersion: 2, MediaType: MediaTypeManifest, Config: cfgDesc, Layers: []Descriptor{d1, d2}}
	manJSON, _ := json.Marshal(man)
	manDesc, err := l.PutBlobBytes(MediaTypeManifest, manJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.WriteIndex(manDesc, "img:latest"); err != nil {
		t.Fatal(err)
	}

	// Reopen from disk (covers OpenLayout) and flatten the named ref.
	opened, err := OpenLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	mem, _, cleanup, err := Flatten(opened, "img:latest", testDeps())
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	defer cleanup()
	root := mem.RootNode()

	etc := child(root, "etc")
	if etc == nil || child(etc, "old") != nil {
		t.Error("etc/old whiteout not applied")
	}
	hosts := child(etc, "hosts")
	if hosts == nil || hosts.Mode&fs.ModeSetuid == 0 {
		t.Errorf("setuid bit lost on hosts: %v", hosts.Mode)
	}
	if string(hosts.Xattrs["user.foo"]) != "bar" {
		t.Errorf("xattr lost: %v", hosts.Xattrs)
	}
	if opq := child(root, "opq"); opq == nil || len(opq.Children) != 0 {
		t.Errorf("opaque whiteout did not clear opq")
	}
	dn := child(child(root, "dev"), "null")
	if dn == nil || dn.Mode&fs.ModeCharDevice == 0 || dn.Rdev != (1<<8|3) {
		t.Errorf("char device wrong: %+v", dn)
	}
	if sda := child(child(root, "dev"), "sda"); sda == nil || sda.Mode&fs.ModeDevice == 0 {
		t.Errorf("block device lost")
	}
	if p := child(root, "run.pipe"); p == nil || p.Mode&fs.ModeNamedPipe == 0 {
		t.Errorf("fifo lost")
	}
	app := child(child(root, "bin"), "app")
	link := child(child(root, "bin"), "applink")
	if app == nil || link == nil || app != link {
		t.Errorf("hard link not shared")
	}
	if got := string(readSource(t, app.Content)); got != "app2" {
		t.Errorf("overwrite lost: app=%q", got)
	}
}

func TestFlattenResolveErrors(t *testing.T) {
	dir := t.TempDir()
	l, _ := CreateLayout(dir)
	// No index yet.
	if _, _, _, err := Flatten(l, "", testDeps()); err == nil {
		t.Error("Flatten without an index should fail")
	}
	// Empty index.
	if err := l.WriteIndex(Descriptor{MediaType: MediaTypeManifest, Digest: "sha256:dead"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Flatten(l, "nope:tag", testDeps()); err == nil {
		t.Error("Flatten with a missing ref should fail")
	}
}

func TestOpenLayoutRejectsNonLayout(t *testing.T) {
	if _, err := OpenLayout(t.TempDir()); err == nil {
		t.Fatal("OpenLayout on a plain dir should fail")
	}
}

// TestBuildSpecialFilesRoundTrip exercises the layer writer's device/fifo/
// symlink/hard-link/xattr branches (writeEntry, devNums) via Build, then
// flattens the result back.
func TestBuildSpecialFilesRoundTrip(t *testing.T) {
	mem := image.NewMem(testDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	root := mem.Root()
	if err := root.Mknod("cdev", 0x0103, meta(fs.ModeCharDevice|0o666)); err != nil {
		t.Fatal(err)
	}
	if err := root.Mknod("bdev", 0x0800, meta(fs.ModeDevice|0o660)); err != nil {
		t.Fatal(err)
	}
	if err := root.Mknod("fifo", 0, meta(fs.ModeNamedPipe|0o644)); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("sl", "cdev", meta(fs.ModeSymlink|0o777)); err != nil {
		t.Fatal(err)
	}
	h, err := root.Create("file", tree.Bytes("data"),
		tree.Meta{Mode: 0o644, ModTime: time.Unix(1_700_000_000, 0).UTC(), Xattrs: map[string][]byte{"user.k": []byte("v")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Link("hardlink", h); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	l, err := CreateLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(l, mem, BuildOptions{Ref: "x:1", Gzip: false}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	flat, _, cleanup, err := Flatten(l, "x:1", testDeps())
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	defer cleanup()
	r := flat.RootNode()

	if d := child(r, "cdev"); d == nil || d.Mode&fs.ModeCharDevice == 0 || d.Rdev != 0x0103 {
		t.Errorf("char device round-trip wrong: %+v", d)
	}
	if d := child(r, "bdev"); d == nil || d.Mode&fs.ModeDevice == 0 {
		t.Errorf("block device lost")
	}
	if p := child(r, "fifo"); p == nil || p.Mode&fs.ModeNamedPipe == 0 {
		t.Errorf("fifo lost")
	}
	if s := child(r, "sl"); s == nil || s.Link != "cdev" {
		t.Errorf("symlink lost")
	}
	f, hl := child(r, "file"), child(r, "hardlink")
	if f == nil || hl == nil || f != hl {
		t.Errorf("hard link not shared on round-trip")
	}
	if string(f.Xattrs["user.k"]) != "v" {
		t.Errorf("xattr lost: %v", f.Xattrs)
	}
}
