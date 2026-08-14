package ociremote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/oci"
)

func TestParseReference(t *testing.T) {
	cases := []struct {
		in                                string
		registry, repository, tag, digest string
	}{
		{"alpine", dockerHubAPI, "library/alpine", "latest", ""},
		{"alpine:3.20", dockerHubAPI, "library/alpine", "3.20", ""},
		{"docker://alpine:3.20", dockerHubAPI, "library/alpine", "3.20", ""},
		{"library/alpine:3.20", dockerHubAPI, "library/alpine", "3.20", ""},
		// A first component with a dot is a host; without one it is a namespace.
		{"myorg/myapp:v1", dockerHubAPI, "myorg/myapp", "v1", ""},
		{"ghcr.io/owner/app:v1", "ghcr.io", "owner/app", "v1", ""},
		{"ghcr.io/owner/app", "ghcr.io", "owner/app", "latest", ""},
		// A port must not be mistaken for a tag.
		{"registry.local:5000/app:v2", "registry.local:5000", "app", "v2", ""},
		{"registry.local:5000/app", "registry.local:5000", "app", "latest", ""},
		{"localhost:5000/app:v2", "localhost:5000", "app", "v2", ""},
		{"localhost/app", "localhost", "app", "latest", ""},
		// Deep repositories.
		{"ghcr.io/a/b/c/d:t", "ghcr.io", "a/b/c/d", "t", ""},
		{
			in: "alpine@sha256:" + strings.Repeat("a", 64), registry: dockerHubAPI,
			repository: "library/alpine", digest: "sha256:" + strings.Repeat("a", 64),
		},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			r, err := ParseReference(tc.in)
			if err != nil {
				t.Fatalf("ParseReference: %v", err)
			}
			if r.Registry != tc.registry || r.Repository != tc.repository ||
				r.Tag != tc.tag || r.Digest != tc.digest {
				t.Errorf("got %+v", r)
			}
		})
	}
}

func TestParseReferenceErrors(t *testing.T) {
	for _, in := range []string{
		"",
		"alpine:",
		"alpine@sha512:abc",
		"alpine@sha256:tooshort",
		"ghcr.io/",
		"a/../b",
		"a/b?x=1",
	} {
		if r, err := ParseReference(in); err == nil {
			t.Errorf("ParseReference(%q) should have failed, got %+v", in, r)
		}
	}
}

func TestReferenceString(t *testing.T) {
	r, _ := ParseReference("ghcr.io/o/a:v1")
	if got := r.String(); got != "ghcr.io/o/a:v1" {
		t.Errorf("String = %q", got)
	}
	r, _ = ParseReference("alpine@sha256:" + strings.Repeat("b", 64))
	if !strings.HasSuffix(r.String(), "@sha256:"+strings.Repeat("b", 64)) {
		t.Errorf("String = %q", r.String())
	}
}

// pull runs a pull against the fake registry.
func pull(t *testing.T, reg *fakeRegistry, ref string, opt Options) (string, string, error) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "layout")
	opt.Insecure = true
	name, err := Pull(reg.host()+"/"+ref, dest, opt)
	return name, dest, err
}

func TestPull(t *testing.T) {
	reg := newFakeRegistry(t)
	man := reg.image("v1", true, oci.MediaTypeLayerTarGz)

	name, dest, err := pull(t, reg, "app:v1", Options{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if name != "app:v1" {
		t.Errorf("reference written as %q", name)
	}

	// The layout must hold exactly the blobs the manifest named, byte for byte.
	l, err := oci.OpenLayout(dest)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := l.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Manifests) != 1 {
		t.Fatalf("index holds %d manifests, want 1", len(idx.Manifests))
	}
	for _, d := range append([]oci.Descriptor{man.Config}, man.Layers...) {
		rc, err := l.BlobReader(d.Digest)
		if err != nil {
			t.Fatalf("blob %s missing from the layout: %v", d.Digest, err)
		}
		rc.Close()
	}
}

// TestPullVerifiesDigests is the property that makes a registry's answer worth
// trusting: content that does not match the name it came under is refused.
func TestPullVerifiesDigests(t *testing.T) {
	reg := newFakeRegistry(t)
	man := reg.image("v1", true, oci.MediaTypeLayerTarGz)
	reg.corrupt = man.Layers[0].Digest

	_, _, err := pull(t, reg, "app:v1", Options{})
	if err == nil {
		t.Fatal("a tampered layer should have been refused")
	}
	if !strings.Contains(err.Error(), "does not match") && !strings.Contains(err.Error(), "bytes") {
		t.Errorf("the error should say the content did not match: %v", err)
	}
}

// TestPullChecksSize covers the other half: a short blob must not pass merely
// because the check ran out of bytes to hash.
func TestPullChecksSize(t *testing.T) {
	reg := newFakeRegistry(t)
	man := reg.image("v1", true, oci.MediaTypeLayerTarGz)
	reg.short = man.Layers[0].Digest

	if _, _, err := pull(t, reg, "app:v1", Options{}); err == nil {
		t.Fatal("a truncated layer should have been refused")
	}
}

func TestPullAuthenticates(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.requireToken = true
	reg.image("v1", true, oci.MediaTypeLayerTarGz)

	if _, _, err := pull(t, reg, "app:v1", Options{}); err != nil {
		t.Fatalf("Pull with a token challenge: %v", err)
	}
	if reg.tokenIssued == 0 {
		t.Error("no token was requested")
	}
	// The token is reused across blobs rather than fetched per request.
	if reg.tokenIssued > 1 {
		t.Errorf("%d tokens issued for one pull; it should be cached", reg.tokenIssued)
	}
}

// TestPullSelectsPlatform covers the index case, which is what a real public
// image is these days.
func TestPullSelectsPlatform(t *testing.T) {
	reg := newFakeRegistry(t)
	amd := reg.image("", true, oci.MediaTypeLayerTarGz)
	amdDigest := reg.addManifest("", amd, oci.MediaTypeManifest)

	armCfg := reg.addBlob([]byte(`{"architecture":"arm64","os":"linux"}`))
	armCfg.MediaType = oci.MediaTypeConfig
	armLayer := reg.addBlob(layer(t, map[string]string{"arm-only": "x"}, true))
	armLayer.MediaType = oci.MediaTypeLayerTarGz
	arm := oci.Manifest{SchemaVersion: 2, MediaType: oci.MediaTypeManifest,
		Config: armCfg, Layers: []oci.Descriptor{armLayer}}
	armDigest := reg.addManifest("", arm, oci.MediaTypeManifest)

	reg.addManifest("multi", oci.Index{
		SchemaVersion: 2, MediaType: oci.MediaTypeIndex,
		Manifests: []oci.Descriptor{
			{MediaType: oci.MediaTypeManifest, Digest: amdDigest,
				Platform: &oci.Platform{OS: "linux", Architecture: "amd64"}},
			{MediaType: oci.MediaTypeManifest, Digest: armDigest,
				Platform: &oci.Platform{OS: "linux", Architecture: "arm64"}},
			// An attestation, which rides in the index and must be skipped.
			{MediaType: oci.MediaTypeManifest, Digest: amdDigest,
				Platform: &oci.Platform{OS: "unknown", Architecture: "unknown"}},
		},
	}, oci.MediaTypeIndex)

	_, dest, err := pull(t, reg, "app:multi", Options{Platform: "linux/arm64"})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	l, err := oci.OpenLayout(dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.BlobReader(armLayer.Digest); err != nil {
		t.Errorf("the arm64 layer was not pulled: %v", err)
	}
	if _, err := l.BlobReader(amd.Layers[0].Digest); err == nil {
		t.Error("the amd64 layer should not have been pulled")
	}

	// A platform the index does not carry must say so, and say what it has.
	_, _, err = pull(t, reg, "app:multi", Options{Platform: "plan9/386"})
	if err == nil {
		t.Fatal("an absent platform should be an error")
	}
	if !strings.Contains(err.Error(), "linux/amd64") {
		t.Errorf("the error should list what is available: %v", err)
	}
	if _, _, err := pull(t, reg, "app:multi", Options{Platform: "nonsense"}); err == nil {
		t.Error("a platform that is not os/arch should be refused")
	}
}

func TestPullErrors(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.image("v1", true, oci.MediaTypeLayerTarGz)

	if _, _, err := pull(t, reg, "app:absent", Options{}); err == nil {
		t.Error("an unknown tag should fail")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("the error should say it was not found: %v", err)
	}
	if _, err := Pull("not a reference!", t.TempDir(), Options{}); err == nil {
		t.Error("an unparsable reference should fail before any request")
	}
}

// TestPullDockerMediaTypes covers what Docker Hub actually serves: its own
// media types, which are not the OCI ones.
func TestPullDockerMediaTypes(t *testing.T) {
	reg := newFakeRegistry(t)
	cfg := reg.addBlob([]byte(`{"architecture":"amd64","os":"linux"}`))
	cfg.MediaType = "application/vnd.docker.container.image.v1+json"
	l := reg.addBlob(layer(t, map[string]string{"etc/hosts": "127.0.0.1\n"}, true))
	l.MediaType = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	reg.addManifest("dockerish", oci.Manifest{
		SchemaVersion: 2, MediaType: dockerManifest, Config: cfg, Layers: []oci.Descriptor{l},
	}, dockerManifest)

	_, dest, err := pull(t, reg, "app:dockerish", Options{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	// The layer must flatten, which is the check that matters: its media type
	// ends in ".tar.gzip" rather than "+gzip", so anything keying off the string
	// would read compressed bytes as a tar.
	l2, err := oci.OpenLayout(dest)
	if err != nil {
		t.Fatal(err)
	}
	mem, _, cleanup, err := oci.Flatten(l2, "", testDeps())
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	defer cleanup()
	etc := childNamed(mem.RootNode(), "etc")
	if etc == nil || childNamed(etc, "hosts") == nil {
		t.Error("the Docker-typed layer did not flatten")
	}
}

func TestPullProgress(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.image("v1", true, oci.MediaTypeLayerTarGz)
	var blobs int
	var bytesSeen int64
	_, _, err := pull(t, reg, "app:v1", Options{
		Progress: func(d oci.Descriptor, done int64) { blobs++; bytesSeen += done },
	})
	if err != nil {
		t.Fatal(err)
	}
	if blobs != 2 { // config plus one layer
		t.Errorf("progress fired %d times, want 2", blobs)
	}
	if bytesSeen == 0 {
		t.Error("progress reported no bytes")
	}
}

func TestPullRejectsBadDestination(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.image("v1", true, oci.MediaTypeLayerTarGz)
	// A destination that is a file, not a directory.
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Pull(reg.host()+"/app:v1", f, Options{Insecure: true}); err == nil {
		t.Error("pulling into a file should fail")
	}
}
