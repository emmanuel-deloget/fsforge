package oci

import (
	"bytes"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func testDeps() image.Deps {
	return image.Deps{
		Clock: image.FixedClock{T: time.Unix(1_700_000_000, 0).UTC()},
		UUID:  image.FixedUUID{},
	}
}

func meta(mode fs.FileMode) tree.Meta {
	return tree.Meta{Mode: mode, UID: 0, GID: 0, ModTime: time.Unix(1_700_000_000, 0).UTC()}
}

// sampleImage builds a representative tree in an image.Mem.
func sampleImage(t *testing.T) *image.Mem {
	t.Helper()
	mem := image.NewMem(testDeps(), tree.Meta{Mode: fs.ModeDir | 0o755})
	root := mem.Root()
	etc, err := root.Mkdir("etc", meta(fs.ModeDir|0o755))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := etc.Create("hosts", tree.Bytes("127.0.0.1 localhost\n"), meta(0o644)); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Mkdir("emptydir", meta(fs.ModeDir|0o700)); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("oci-layer-data!\n"), 5000)
	if _, err := root.Create("data.bin", tree.Bytes(big), meta(0o600)); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("link", "etc/hosts", meta(fs.ModeSymlink|0o777)); err != nil {
		t.Fatal(err)
	}
	return mem
}

func buildInto(t *testing.T, dir string, gzip bool) (*Layout, Descriptor) {
	t.Helper()
	l, err := CreateLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	desc, err := Build(l, sampleImage(t), BuildOptions{
		Ref:    "fsforge:test",
		Gzip:   gzip,
		Config: RunConfig{Env: []string{"PATH=/bin"}, Cmd: []string{"/bin/true"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return l, desc
}

func readSource(t *testing.T, s tree.Source) []byte {
	t.Helper()
	buf := make([]byte, s.Size())
	if _, err := s.ReadAt(buf, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	return buf
}

func child(n *image.Node, name string) *image.Node {
	for _, e := range n.Children {
		if e.Name == name {
			return e.Node
		}
	}
	return nil
}

func TestBuildFlattenRoundTrip(t *testing.T) {
	for _, gz := range []bool{false, true} {
		dir := t.TempDir()
		l, _ := buildInto(t, dir, gz)

		mem, cfg, cleanup, err := Flatten(l, "fsforge:test", testDeps())
		if err != nil {
			t.Fatalf("gzip=%v Flatten: %v", gz, err)
		}
		defer cleanup()

		if cfg.RootFS.Type != "layers" || len(cfg.RootFS.DiffIDs) != 1 {
			t.Errorf("gzip=%v bad rootfs: %+v", gz, cfg.RootFS)
		}
		if len(cfg.Config.Cmd) != 1 || cfg.Config.Cmd[0] != "/bin/true" {
			t.Errorf("gzip=%v cmd not preserved: %+v", gz, cfg.Config.Cmd)
		}

		root := mem.RootNode()
		etc := child(root, "etc")
		if etc == nil || !etc.IsDir() {
			t.Fatalf("gzip=%v etc missing", gz)
		}
		if got := string(readSource(t, child(etc, "hosts").Content)); got != "127.0.0.1 localhost\n" {
			t.Errorf("gzip=%v hosts=%q", gz, got)
		}
		if ed := child(root, "emptydir"); ed == nil || !ed.IsDir() {
			t.Errorf("gzip=%v emptydir lost", gz)
		}
		want := bytes.Repeat([]byte("oci-layer-data!\n"), 5000)
		if got := readSource(t, child(root, "data.bin").Content); !bytes.Equal(got, want) {
			t.Errorf("gzip=%v data.bin mismatch: %d vs %d", gz, len(got), len(want))
		}
		if ln := child(root, "link"); ln == nil || ln.Link != "etc/hosts" {
			t.Errorf("gzip=%v symlink lost", gz)
		}
	}
}

func TestReproducible(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	_, m1 := buildInto(t, d1, true)
	_, m2 := buildInto(t, d2, true)
	if m1.Digest != m2.Digest {
		t.Fatalf("manifest digests differ: %s vs %s", m1.Digest, m2.Digest)
	}
	// Every blob file must exist identically by content address.
	matches, _ := filepath.Glob(filepath.Join(d1, "blobs", "sha256", "*"))
	if len(matches) != 3 { // layer + config + manifest
		t.Errorf("expected 3 blobs, got %d", len(matches))
	}
}

func TestLayerDigests(t *testing.T) {
	dir := t.TempDir()
	l, manDesc := buildInto(t, dir, true)
	var man Manifest
	if err := l.BlobJSON(manDesc.Digest, &man); err != nil {
		t.Fatal(err)
	}
	if len(man.Layers) != 1 || man.Layers[0].MediaType != MediaTypeLayerTarGz {
		t.Fatalf("bad layers: %+v", man.Layers)
	}
	var cfg Image
	if err := l.BlobJSON(man.Config.Digest, &cfg); err != nil {
		t.Fatal(err)
	}
	// diff_id is the uncompressed digest; the layer blob digest is compressed,
	// so they must differ for a gzip layer.
	if cfg.RootFS.DiffIDs[0] == man.Layers[0].Digest {
		t.Errorf("diff_id should differ from compressed layer digest")
	}
}
