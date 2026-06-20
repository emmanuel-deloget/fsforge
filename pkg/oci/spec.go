package oci

// OCI image-spec media types (v1).
const (
	MediaTypeLayout      = "1.0.0"
	MediaTypeIndex       = "application/vnd.oci.image.index.v1+json"
	MediaTypeManifest    = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeConfig      = "application/vnd.oci.image.config.v1+json"
	MediaTypeLayerTarGz  = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeLayerTar    = "application/vnd.oci.image.layer.v1.tar"
	MediaTypeLayerTarZst = "application/vnd.oci.image.layer.v1.tar+zstd"

	annotationRefName = "org.opencontainers.image.ref.name"
)

// Descriptor points at a content-addressed blob.
type Descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"` // "sha256:<hex>"
	Size        int64             `json:"size"`
	Platform    *Platform         `json:"platform,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Platform describes the target of a manifest within an index.
type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant,omitempty"`
}

// Index is the top-level index.json.
type Index struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Manifests     []Descriptor `json:"manifests"`
}

// Manifest ties a config blob to its ordered layer blobs.
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

// Image is the runtime config blob (application/vnd.oci.image.config.v1+json).
type Image struct {
	Created      string    `json:"created,omitempty"`
	Architecture string    `json:"architecture"`
	OS           string    `json:"os"`
	Config       RunConfig `json:"config"`
	RootFS       RootFS    `json:"rootfs"`
	History      []History `json:"history,omitempty"`
}

// RunConfig is the container runtime configuration subset fsforge sets.
type RunConfig struct {
	Env        []string            `json:"Env,omitempty"`
	Entrypoint []string            `json:"Entrypoint,omitempty"`
	Cmd        []string            `json:"Cmd,omitempty"`
	WorkingDir string              `json:"WorkingDir,omitempty"`
	Labels     map[string]string   `json:"Labels,omitempty"`
	Volumes    map[string]struct{} `json:"Volumes,omitempty"`
}

// RootFS lists the uncompressed layer digests (diff_ids) in order.
type RootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

// History records one build step; fsforge emits one entry per layer.
type History struct {
	Created    string `json:"created,omitempty"`
	CreatedBy  string `json:"created_by,omitempty"`
	EmptyLayer bool   `json:"empty_layer,omitempty"`
}

// layerMediaType reports the layer media type for a compression choice.
func layerMediaType(gzip bool) string {
	if gzip {
		return MediaTypeLayerTarGz
	}
	return MediaTypeLayerTar
}
