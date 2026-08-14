package ociremote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/pkg/oci"
)

// fakeRegistry is enough of an OCI distribution registry to pull from: blobs by
// digest, manifests by tag or digest, and an optional token challenge. Tests
// build one per case so nothing here needs the network — a test that reaches
// Docker Hub is a test that fails on a train.
type fakeRegistry struct {
	t         *testing.T
	blobs     map[string][]byte // digest -> content
	manifests map[string][]byte // tag or digest -> document
	types     map[string]string // reference -> content type
	server    *httptest.Server

	// requireToken makes every request answer 401 until a token is presented,
	// which is what a real registry does.
	requireToken bool
	tokenIssued  int
	// corrupt rewrites one blob's bytes on the way out, to check the digest
	// verification actually verifies.
	corrupt string
	// short truncates a blob, for the size check.
	short string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	r := &fakeRegistry{
		t:         t,
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		types:     map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", r.handleToken)
	mux.HandleFunc("/v2/", r.handleV2)
	r.server = httptest.NewServer(mux)
	t.Cleanup(r.server.Close)
	return r
}

func (r *fakeRegistry) host() string { return strings.TrimPrefix(r.server.URL, "http://") }

func (r *fakeRegistry) handleToken(w http.ResponseWriter, req *http.Request) {
	if req.URL.Query().Get("scope") == "" {
		r.t.Errorf("token request carried no scope: %s", req.URL)
	}
	r.tokenIssued++
	json.NewEncoder(w).Encode(map[string]string{"token": "issued-token"})
}

func (r *fakeRegistry) handleV2(w http.ResponseWriter, req *http.Request) {
	if r.requireToken && req.Header.Get("Authorization") != "Bearer issued-token" {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer realm="%s/token",service="fake",scope="repository:x:pull"`, r.server.URL))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/v2/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, req)
		return
	}
	kind, reference := parts[len(parts)-2], parts[len(parts)-1]

	switch kind {
	case "manifests":
		body, ok := r.manifests[reference]
		if !ok {
			http.NotFound(w, req)
			return
		}
		if ct := r.types[reference]; ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Write(body)
	case "blobs":
		body, ok := r.blobs[reference]
		if !ok {
			http.NotFound(w, req)
			return
		}
		switch reference {
		case r.corrupt:
			body = append([]byte("tampered"), body...)
		case r.short:
			body = body[:len(body)/2]
		}
		w.Write(body)
	default:
		http.NotFound(w, req)
	}
}

func (r *fakeRegistry) addBlob(b []byte) oci.Descriptor {
	sum := sha256.Sum256(b)
	d := "sha256:" + hex.EncodeToString(sum[:])
	r.blobs[d] = b
	return oci.Descriptor{Digest: d, Size: int64(len(b))}
}

func (r *fakeRegistry) addManifest(tag string, doc any, mediaType string) string {
	b, err := json.Marshal(doc)
	if err != nil {
		r.t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	r.manifests[digest] = b
	r.types[digest] = mediaType
	if tag != "" {
		r.manifests[tag] = b
		r.types[tag] = mediaType
	}
	return digest
}

// layer builds a gzipped tar holding the named files.
func layer(t *testing.T, files map[string]string, gzipped bool) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sortStrings(names)
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
	if !gzipped {
		return raw.Bytes()
	}
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	gw.Write(raw.Bytes())
	gw.Close()
	return out.Bytes()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// image populates the registry with a single-platform image and returns its tag.
func (r *fakeRegistry) image(tag string, gzipped bool, mediaType string) oci.Manifest {
	cfg := r.addBlob([]byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`))
	cfg.MediaType = oci.MediaTypeConfig
	l := r.addBlob(layer(r.t, map[string]string{"etc/hosts": "127.0.0.1\n", "bin/sh": "#!/bin/sh\n"}, gzipped))
	l.MediaType = mediaType

	man := oci.Manifest{SchemaVersion: 2, MediaType: oci.MediaTypeManifest, Config: cfg, Layers: []oci.Descriptor{l}}
	r.addManifest(tag, man, oci.MediaTypeManifest)
	return man
}
