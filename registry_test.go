package fsforge_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/pkg/oci"
	"github.com/emmanuel-deloget/fsforge/pkg/ociremote"
)

// TestConvertFromRegistry is the workflow the registry client exists for, whole:
// a reference goes in, a filesystem image comes out, and nothing in between
// touched a disk the caller had to prepare.
//
// The registry is a test server rather than a real one. A test that reaches
// Docker Hub fails on a train, fails behind a proxy, and passes for reasons
// that have nothing to do with the code.
func TestConvertFromRegistry(t *testing.T) {
	reg := startRegistry(t)

	out := filepath.Join(t.TempDir(), "rootfs.sqsh")
	err := fsforge.Convert(
		fsforge.Location{Kind: "docker", Path: reg + "/app:v1"},
		fsforge.Location{Kind: "squashfs", Path: out},
		fsforge.Options{Deps: fsforge.ReproducibleDeps(1600000000),
			Registry: ociremote.Options{Insecure: true}},
	)
	if err != nil {
		t.Fatalf("convert from a registry: %v", err)
	}

	root := readBack(t, "squashfs", out)
	if n := at(root, "etc/hosts"); n == nil {
		t.Error("etc/hosts did not survive the pull")
	}
	// The second layer overwrote a file from the first, which is what makes it a
	// flatten rather than a concatenation.
	greeting := at(root, "etc/greeting")
	if greeting == nil {
		t.Fatal("etc/greeting missing")
	}
	if got := string(readBytes(t, greeting.Content)); got != "second\n" {
		t.Errorf("etc/greeting = %q, want the second layer's content", got)
	}
	// A whiteout in the second layer must have removed the first layer's file.
	if at(root, "etc/removed") != nil {
		t.Error("a whiteout did not delete the file it names")
	}
}

func TestConvertFromRegistryReportsFailures(t *testing.T) {
	reg := startRegistry(t)
	err := fsforge.Convert(
		fsforge.Location{Kind: "docker", Path: reg + "/app:nosuchtag"},
		fsforge.Location{Kind: "squashfs", Path: filepath.Join(t.TempDir(), "x.sqsh")},
		fsforge.Options{Registry: ociremote.Options{Insecure: true}},
	)
	if err == nil {
		t.Fatal("an unknown tag should fail the conversion")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("the error should say what went wrong: %v", err)
	}
}

// startRegistry serves one two-layer image and returns its host:port. Insecure
// HTTP is what a test server speaks, which is why the docker kind accepts it
// for a host that is plainly local.
func startRegistry(t *testing.T) string {
	t.Helper()
	blobs := map[string][]byte{}
	manifests := map[string][]byte{}

	add := func(b []byte) oci.Descriptor {
		sum := sha256.Sum256(b)
		d := "sha256:" + hex.EncodeToString(sum[:])
		blobs[d] = b
		return oci.Descriptor{Digest: d, Size: int64(len(b))}
	}

	cfg := add([]byte(`{"architecture":"amd64","os":"linux"}`))
	cfg.MediaType = oci.MediaTypeConfig
	first := add(tarGz(t, map[string]string{
		"etc/hosts":    "127.0.0.1 localhost\n",
		"etc/greeting": "first\n",
		"etc/removed":  "gone soon\n",
	}))
	first.MediaType = oci.MediaTypeLayerTarGz
	second := add(tarGz(t, map[string]string{
		"etc/greeting":    "second\n",
		"etc/.wh.removed": "",
	}))
	second.MediaType = oci.MediaTypeLayerTarGz

	man, err := json.Marshal(oci.Manifest{
		SchemaVersion: 2, MediaType: oci.MediaTypeManifest,
		Config: cfg, Layers: []oci.Descriptor{first, second},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifests["v1"] = man

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v2/"), "/")
		if len(parts) < 2 {
			http.NotFound(w, r)
			return
		}
		kind, ref := parts[len(parts)-2], parts[len(parts)-1]
		var body []byte
		var ok bool
		switch kind {
		case "manifests":
			body, ok = manifests[ref]
			if ok {
				w.Header().Set("Content-Type", oci.MediaTypeManifest)
			}
		case "blobs":
			body, ok = blobs[ref]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	for _, name := range names {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	gw.Write(raw.Bytes())
	gw.Close()
	return out.Bytes()
}

func readBytes(t *testing.T, s interface {
	ReadAt([]byte, int64) (int, error)
	Size() int64
}) []byte {
	t.Helper()
	if s == nil || s.Size() == 0 {
		return nil
	}
	b := make([]byte, s.Size())
	if _, err := s.ReadAt(b, 0); err != nil {
		t.Fatal(err)
	}
	return b
}
