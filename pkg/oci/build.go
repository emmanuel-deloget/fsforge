package oci

import (
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// BuildOptions configures an image built from a tree.
type BuildOptions struct {
	Ref          string    // index tag, e.g. "myimage:latest" (optional)
	Architecture string    // default "amd64"
	OS           string    // default "linux"
	Config       RunConfig // runtime config (Env, Entrypoint, Cmd, …)
	Gzip         bool      // gzip-compress the layer (recommended)
	Created      time.Time // override the timestamp; zero uses the image clock
}

// Build serialises img's tree as a single-layer OCI image into dst and writes
// the index. It returns the manifest descriptor. The build is deterministic
// when img is wired with a fixed clock.
func Build(dst *Layout, img *image.Mem, opt BuildOptions) (Descriptor, error) {
	if opt.Architecture == "" {
		opt.Architecture = "amd64"
	}
	if opt.OS == "" {
		opt.OS = "linux"
	}
	created := opt.Created
	if created.IsZero() {
		created = img.Deps().Clock.Now()
	}
	createdStr := created.UTC().Format(time.RFC3339)

	layerDesc, diffID, err := writeLayer(dst, img.RootNode(), opt.Gzip)
	if err != nil {
		return Descriptor{}, err
	}

	cfg := Image{
		Created:      createdStr,
		Architecture: opt.Architecture,
		OS:           opt.OS,
		Config:       opt.Config,
		RootFS:       RootFS{Type: "layers", DiffIDs: []string{diffID}},
		History:      []History{{Created: createdStr, CreatedBy: "fsforge"}},
	}
	cfgBytes, err := marshalJSON(cfg)
	if err != nil {
		return Descriptor{}, err
	}
	cfgDesc, err := dst.PutBlobBytes(MediaTypeConfig, cfgBytes)
	if err != nil {
		return Descriptor{}, err
	}

	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeManifest,
		Config:        cfgDesc,
		Layers:        []Descriptor{layerDesc},
	}
	manBytes, err := marshalJSON(manifest)
	if err != nil {
		return Descriptor{}, err
	}
	manDesc, err := dst.PutBlobBytes(MediaTypeManifest, manBytes)
	if err != nil {
		return Descriptor{}, err
	}
	manDesc.Platform = &Platform{Architecture: opt.Architecture, OS: opt.OS}

	if err := dst.WriteIndex(manDesc, opt.Ref); err != nil {
		return Descriptor{}, err
	}
	return manDesc, nil
}
