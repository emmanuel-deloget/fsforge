// Package ociremote pulls images from an OCI distribution registry into a local
// OCI layout, using nothing but the standard library.
//
// This is the only package in fsforge that touches the network, deliberately:
// the engines and the tree model stay offline, and a caller that never mentions
// a registry never opens a socket. What arrives here is an ordinary layout on
// disk, so everything downstream — flatten, convert, the engines — works on it
// unchanged and unaware.
//
// Every blob is verified against the digest that named it before it is kept.
// A registry is a remote party; content addressing is the only thing that makes
// its answers checkable, and skipping the check would leave nothing between a
// build and whatever a proxy felt like returning.
package ociremote

import (
	"fmt"
	"strings"
)

const (
	// dockerHub is where a reference with no registry comes from, and the host
	// that serves it is not the one people write.
	dockerHub      = "docker.io"
	dockerHubAPI   = "registry-1.docker.io"
	defaultTag     = "latest"
	officialPrefix = "library/"
)

// Reference is a parsed image reference: where to ask, what to ask for, and
// which version of it.
type Reference struct {
	// Registry is the host to talk to, already resolved to the API endpoint.
	Registry string
	// Repository is the path within the registry, e.g. "library/alpine".
	Repository string
	// Tag names a mutable version; empty when Digest is set.
	Tag string
	// Digest pins an exact one, e.g. "sha256:…"; empty when Tag is set.
	Digest string
}

// String renders the reference back, canonically.
func (r Reference) String() string {
	s := r.Registry + "/" + r.Repository
	if r.Digest != "" {
		return s + "@" + r.Digest
	}
	return s + ":" + r.Tag
}

// ParseReference reads the shorthand people actually type.
//
// The rules are Docker's, and the awkward one is telling a registry from a
// repository: "alpine/git" is a repository on Docker Hub, while
// "ghcr.io/user" is a registry and a repository. What separates them is
// whether the first component looks like a host — it holds a dot or a colon, or
// it is localhost.
//
// A "docker://" prefix is accepted and ignored, since that is how the CLI and
// most other tools spell it.
func ParseReference(s string) (Reference, error) {
	s = strings.TrimPrefix(s, "docker://")
	s = strings.TrimPrefix(s, "oci://")
	if s == "" {
		return Reference{}, fmt.Errorf("ociremote: empty image reference")
	}

	var r Reference
	rest := s

	// Split off the digest or tag first, so a registry's port is not mistaken
	// for one. A colon belongs to a tag only if it comes after the last slash.
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		r.Digest = rest[i+1:]
		rest = rest[:i]
		if !strings.HasPrefix(r.Digest, "sha256:") || len(r.Digest) != len("sha256:")+64 {
			return Reference{}, fmt.Errorf("ociremote: unsupported digest %q (want sha256:<64 hex>)", r.Digest)
		}
	} else if i := strings.LastIndex(rest, ":"); i >= 0 && !strings.Contains(rest[i+1:], "/") {
		r.Tag = rest[i+1:]
		rest = rest[:i]
		if r.Tag == "" {
			return Reference{}, fmt.Errorf("ociremote: empty tag in %q", s)
		}
	}

	// Now decide whether the leading component is a registry.
	r.Registry = dockerHubAPI
	if i := strings.Index(rest, "/"); i >= 0 {
		head := rest[:i]
		if strings.ContainsAny(head, ".:") || head == "localhost" {
			r.Registry = head
			rest = rest[i+1:]
		}
	}
	if rest == "" {
		return Reference{}, fmt.Errorf("ociremote: no repository in %q", s)
	}
	// Docker Hub keeps its own images under library/, which nobody types.
	if r.Registry == dockerHubAPI && !strings.Contains(rest, "/") {
		rest = officialPrefix + rest
	}
	r.Repository = rest

	if r.Tag == "" && r.Digest == "" {
		r.Tag = defaultTag
	}
	if err := validRepository(r.Repository); err != nil {
		return Reference{}, err
	}
	return r, nil
}

// validRepository rejects what would otherwise be pasted into a URL path. A
// reference is caller input; a repository holding ".." or a query separator has
// no business reaching a request line.
func validRepository(repo string) error {
	if repo == "" {
		return fmt.Errorf("ociremote: empty repository")
	}
	for _, part := range strings.Split(repo, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("ociremote: bad repository component %q", part)
		}
	}
	if strings.ContainsAny(repo, "?#\\ ") {
		return fmt.Errorf("ociremote: repository %q holds a character a URL path cannot carry", repo)
	}
	return nil
}

// reference returns the tag or digest to put in a URL.
func (r Reference) reference() string {
	if r.Digest != "" {
		return r.Digest
	}
	return r.Tag
}
