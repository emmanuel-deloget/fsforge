package ociremote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/oci"
)

// Docker's own media types, which predate the OCI ones and are still what
// Docker Hub serves for most images. They mean the same things.
const (
	dockerManifest     = "application/vnd.docker.distribution.manifest.v2+json"
	dockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
)

// acceptTypes is what a manifest request asks for, most preferred first.
var acceptTypes = strings.Join([]string{
	oci.MediaTypeIndex,
	oci.MediaTypeManifest,
	dockerManifestList,
	dockerManifest,
}, ", ")

// Options tunes a pull.
type Options struct {
	// Platform selects a manifest out of a multi-platform index, as
	// "os/architecture" — "linux/arm64". Empty means linux and the architecture
	// this binary was built for, which is what somebody building a rootfs on the
	// machine that will run it means.
	Platform string
	// Username and Password authenticate to registries that ask. Anonymous
	// pulls, which is most public images, need neither.
	Username string
	Password string
	// HTTPClient overrides the client used, for callers with their own timeouts,
	// proxy or transport. Nil means a client with a sensible timeout.
	HTTPClient *http.Client
	// Insecure allows plain HTTP, for a registry on a private network or in a
	// test. It is off by default: a pull over HTTP is a pull anybody on the path
	// can rewrite, and the digest checks only prove the bytes match what the
	// manifest said — which arrived the same way.
	Insecure bool
	// Progress, if set, is called as each blob is fetched.
	Progress func(desc oci.Descriptor, done int64)
}

// Pull fetches an image into a fresh OCI layout at dest, and returns the
// reference under which it was written.
//
// What lands on disk is an ordinary layout, so the rest of fsforge neither
// knows nor cares that it came from a registry:
//
//	ref, _ := ociremote.Pull("alpine:3.20", "./alpine-oci", ociremote.Options{})
//	fsforge.Convert(
//	    fsforge.Location{Kind: "oci", Path: "./alpine-oci"},
//	    fsforge.Location{Kind: "ext4", Path: "rootfs.img"},
//	    fsforge.Options{Size: "256M"})
func Pull(ref string, dest string, opt Options) (string, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return "", err
	}
	c := newClient(r, opt)

	man, manBytes, err := c.fetchManifest(r.reference())
	if err != nil {
		return "", err
	}

	layout, err := oci.CreateLayout(dest)
	if err != nil {
		return "", err
	}

	// The config and the layers are copied verbatim, digests intact, so the
	// layout holds exactly what the registry served.
	if err := c.copyBlob(layout, man.Config); err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	for i, layer := range man.Layers {
		if err := c.copyBlob(layout, layer); err != nil {
			return "", fmt.Errorf("layer %d of %d: %w", i+1, len(man.Layers), err)
		}
	}

	manDesc, err := layout.PutBlobBytes(manifestMediaType(man), manBytes)
	if err != nil {
		return "", err
	}
	refName := r.Repository
	if r.Tag != "" {
		refName += ":" + r.Tag
	}
	if err := layout.WriteIndex(manDesc, refName); err != nil {
		return "", err
	}
	return refName, nil
}

func manifestMediaType(m oci.Manifest) string {
	if m.MediaType != "" {
		return m.MediaType
	}
	return oci.MediaTypeManifest
}

type client struct {
	ref    Reference
	auth   *authenticator
	opt    Options
	scheme string
	scope  string
}

func newClient(r Reference, opt Options) *client {
	hc := opt.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	scheme := "https"
	if opt.Insecure {
		scheme = "http"
	}
	return &client{
		ref:    r,
		auth:   newAuthenticator(hc, opt.Username, opt.Password),
		opt:    opt,
		scheme: scheme,
		scope:  "repository:" + r.Repository + ":pull",
	}
}

func (c *client) url(kind, reference string) string {
	return fmt.Sprintf("%s://%s/v2/%s/%s/%s", c.scheme, c.ref.Registry, c.ref.Repository, kind, reference)
}

// fetchManifest returns the image manifest for a reference, resolving an index
// to the manifest for the wanted platform.
func (c *client) fetchManifest(reference string) (oci.Manifest, []byte, error) {
	body, mediaType, err := c.get("manifests", reference, acceptTypes)
	if err != nil {
		return oci.Manifest{}, nil, err
	}

	// An index and a manifest are told apart by what is inside, not by the
	// Content-Type: registries have been known to answer with the type that was
	// asked for rather than the one they hold.
	var probe struct {
		MediaType string           `json:"mediaType"`
		Manifests []oci.Descriptor `json:"manifests"`
		Config    *oci.Descriptor  `json:"config"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return oci.Manifest{}, nil, fmt.Errorf("ociremote: unreadable manifest: %w", err)
	}

	if len(probe.Manifests) > 0 && probe.Config == nil {
		chosen, err := c.selectPlatform(probe.Manifests)
		if err != nil {
			return oci.Manifest{}, nil, err
		}
		return c.fetchManifest(chosen.Digest)
	}

	var man oci.Manifest
	if err := json.Unmarshal(body, &man); err != nil {
		return oci.Manifest{}, nil, fmt.Errorf("ociremote: unreadable manifest: %w", err)
	}
	if man.Config.Digest == "" || len(man.Layers) == 0 {
		return oci.Manifest{}, nil, fmt.Errorf("ociremote: %s served a manifest with no %s",
			c.ref.Registry, map[bool]string{true: "config", false: "layers"}[man.Config.Digest == ""])
	}
	if man.MediaType == "" {
		man.MediaType = mediaType
	}
	return man, body, nil
}

// selectPlatform picks the manifest matching the wanted os/architecture.
func (c *client) selectPlatform(manifests []oci.Descriptor) (oci.Descriptor, error) {
	wantOS, wantArch := "linux", runtime.GOARCH
	if c.opt.Platform != "" {
		os, arch, ok := strings.Cut(c.opt.Platform, "/")
		if !ok {
			return oci.Descriptor{}, fmt.Errorf("ociremote: platform %q is not os/architecture", c.opt.Platform)
		}
		wantOS, wantArch = os, arch
	}
	var available []string
	for _, m := range manifests {
		if m.Platform == nil {
			continue
		}
		// Attestations ride in the same index as the images they describe and
		// have no architecture of their own.
		if m.Platform.Architecture == "unknown" || m.Platform.OS == "unknown" {
			continue
		}
		if m.Platform.OS == wantOS && m.Platform.Architecture == wantArch {
			return m, nil
		}
		available = append(available, m.Platform.OS+"/"+m.Platform.Architecture)
	}
	return oci.Descriptor{}, fmt.Errorf("ociremote: %s has no %s/%s image (it has %s)",
		c.ref, wantOS, wantArch, strings.Join(available, ", "))
}

// copyBlob streams one blob into the layout, verifying its digest as it goes.
func (c *client) copyBlob(layout *oci.Layout, desc oci.Descriptor) error {
	req, err := c.request("blobs", desc.Digest, "")
	if err != nil {
		return err
	}
	resp, err := c.auth.do(req, c.scope)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ociremote: fetching %s returned %s", desc.Digest, resp.Status)
	}

	want, err := parseDigest(desc.Digest)
	if err != nil {
		return err
	}
	h := sha256.New()
	var read int64
	_, err = layout.PutBlobStream(desc.MediaType, func(w io.Writer) error {
		// Bounded by the size the manifest declared: a registry that keeps
		// sending would otherwise fill the disk before the digest check ever ran.
		lr := io.LimitReader(resp.Body, desc.Size+1)
		n, err := io.Copy(io.MultiWriter(w, h), lr)
		read = n
		return err
	})
	if err != nil {
		return err
	}
	if desc.Size > 0 && read != desc.Size {
		return fmt.Errorf("ociremote: %s is %d bytes, manifest said %d", desc.Digest, read, desc.Size)
	}
	if got := h.Sum(nil); !equalDigest(got, want) {
		return fmt.Errorf("ociremote: %s does not match its content (got sha256:%s)",
			desc.Digest, hex.EncodeToString(got))
	}
	if c.opt.Progress != nil {
		c.opt.Progress(desc, read)
	}
	return nil
}

func (c *client) request(kind, reference, accept string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, c.url(kind, reference), nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", "fsforge")
	return req, nil
}

// get fetches a document, returning its bytes and content type.
func (c *client) get(kind, reference, accept string) ([]byte, string, error) {
	req, err := c.request(kind, reference, accept)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.auth.do(req, c.scope)
	if err != nil {
		return nil, "", err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, "", fmt.Errorf("ociremote: %s not found in %s", reference, c.ref.Registry)
	default:
		return nil, "", fmt.Errorf("ociremote: %s returned %s for %s", c.ref.Registry, resp.Status, reference)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, "", err
	}
	// A manifest fetched by digest is checkable the same way a blob is.
	if strings.HasPrefix(reference, "sha256:") {
		want, err := parseDigest(reference)
		if err != nil {
			return nil, "", err
		}
		sum := sha256.Sum256(body)
		if !equalDigest(sum[:], want) {
			return nil, "", fmt.Errorf("ociremote: %s does not match its content", reference)
		}
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func parseDigest(d string) ([]byte, error) {
	hexPart, ok := strings.CutPrefix(d, "sha256:")
	if !ok {
		return nil, fmt.Errorf("ociremote: unsupported digest algorithm in %q", d)
	}
	b, err := hex.DecodeString(hexPart)
	if err != nil || len(b) != sha256.Size {
		return nil, fmt.Errorf("ociremote: malformed digest %q", d)
	}
	return b, nil
}

func equalDigest(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
