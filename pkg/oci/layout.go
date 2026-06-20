package oci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Layout is a writable OCI image layout on disk: an oci-layout marker, an
// index.json, and content-addressed blobs under blobs/sha256/.
type Layout struct {
	root string
}

// CreateLayout initialises an empty OCI layout at root, creating directories
// and the oci-layout marker.
func CreateLayout(root string) (*Layout, error) {
	if err := os.MkdirAll(filepath.Join(root, "blobs", "sha256"), 0o755); err != nil {
		return nil, err
	}
	marker := struct {
		Version string `json:"imageLayoutVersion"`
	}{MediaTypeLayout}
	if err := writeJSON(filepath.Join(root, "oci-layout"), marker); err != nil {
		return nil, err
	}
	return &Layout{root: root}, nil
}

// OpenLayout opens an existing layout for reading.
func OpenLayout(root string) (*Layout, error) {
	if _, err := os.Stat(filepath.Join(root, "oci-layout")); err != nil {
		return nil, fmt.Errorf("oci: not an image layout: %w", err)
	}
	return &Layout{root: root}, nil
}

func (l *Layout) blobPath(digest string) string {
	return filepath.Join(l.root, "blobs", "sha256", digestHex(digest))
}

// BlobReader opens a blob for reading.
func (l *Layout) BlobReader(digest string) (io.ReadCloser, error) {
	return os.Open(l.blobPath(digest))
}

// PutBlobBytes stores b as a blob and returns its descriptor with mediaType.
func (l *Layout) PutBlobBytes(mediaType string, b []byte) (Descriptor, error) {
	sum := sha256.Sum256(b)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := os.WriteFile(l.blobPath(digest), b, 0o644); err != nil {
		return Descriptor{}, err
	}
	return Descriptor{MediaType: mediaType, Digest: digest, Size: int64(len(b))}, nil
}

// PutBlobStream stores whatever write emits, hashing as it streams to a temp
// file (never buffering the whole blob), then renames it into place. It returns
// the stored blob's descriptor.
func (l *Layout) PutBlobStream(mediaType string, write func(io.Writer) error) (Descriptor, error) {
	tmp, err := os.CreateTemp(filepath.Join(l.root, "blobs", "sha256"), ".tmp-*")
	if err != nil {
		return Descriptor{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	h := sha256.New()
	counter := &countingWriter{}
	if err := write(io.MultiWriter(tmp, h, counter)); err != nil {
		tmp.Close()
		return Descriptor{}, err
	}
	if err := tmp.Close(); err != nil {
		return Descriptor{}, err
	}
	digest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if err := os.Rename(tmpName, l.blobPath(digest)); err != nil {
		return Descriptor{}, err
	}
	return Descriptor{MediaType: mediaType, Digest: digest, Size: counter.n}, nil
}

// WriteIndex writes index.json referencing a single manifest, tagged refName
// (may be empty).
func (l *Layout) WriteIndex(manifest Descriptor, refName string) error {
	if refName != "" {
		if manifest.Annotations == nil {
			manifest.Annotations = map[string]string{}
		}
		manifest.Annotations[annotationRefName] = refName
	}
	idx := Index{SchemaVersion: 2, MediaType: MediaTypeIndex, Manifests: []Descriptor{manifest}}
	return writeJSON(filepath.Join(l.root, "index.json"), idx)
}

// Index reads index.json.
func (l *Layout) Index() (Index, error) {
	var idx Index
	b, err := os.ReadFile(filepath.Join(l.root, "index.json"))
	if err != nil {
		return idx, err
	}
	return idx, json.Unmarshal(b, &idx)
}

// BlobJSON reads a blob and unmarshals it into v.
func (l *Layout) BlobJSON(digest string, v any) error {
	b, err := os.ReadFile(l.blobPath(digest))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := marshalJSON(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// marshalJSON produces canonical JSON: struct field order is stable and map
// keys are sorted by encoding/json, so identical inputs yield identical blobs.
func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }

func digestHex(digest string) string {
	if i := indexByte(digest, ':'); i >= 0 {
		return digest[i+1:]
	}
	return digest
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}
