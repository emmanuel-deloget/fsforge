package oci

import (
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// AddLayer stacks img's tree as a new, additive layer on top of the image
// tagged baseRef in dst (or the first manifest when baseRef is empty), and
// writes an updated config, manifest and index. It returns the new manifest
// descriptor.
//
// The layer is *additive*: it is a full tar of img's tree, unioned over the
// existing layers by the normal overlay rules. Files it contains are added or
// overwritten; files it does not mention are inherited unchanged from the lower
// layers. It records no whiteouts, so it cannot delete a path — use
// AddLayerDiff for that.
//
// The base image's architecture, OS and runtime configuration are preserved;
// from opt only Ref (the tag for the resulting index, defaulting to baseRef),
// Gzip (layer compression) and Created (the history timestamp, defaulting to
// img's injected clock) are used. The result is deterministic when img is wired
// with a fixed clock.
func AddLayer(dst *Layout, baseRef string, img *image.Mem, opt BuildOptions) (Descriptor, error) {
	man, cfg, err := dst.resolve(baseRef)
	if err != nil {
		return Descriptor{}, err
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

	// Append the layer to the manifest, its diff_id to the config's rootfs, and
	// a history entry. Order matters: layers and diff_ids are bottom-to-top.
	cfg.Created = createdStr
	cfg.RootFS.DiffIDs = append(cfg.RootFS.DiffIDs, diffID)
	cfg.History = append(cfg.History, History{Created: createdStr, CreatedBy: "fsforge"})

	ref := opt.Ref
	if ref == "" {
		ref = baseRef
	}
	return writeImageMeta(dst, append(man.Layers, layerDesc), cfg, ref)
}

// AddLayerDiff stacks a layer that is the *delta* from the image tagged baseRef
// to img's tree, and writes an updated config, manifest and index. It returns
// the new manifest descriptor.
//
// Unlike AddLayer, this records deletions: it flattens the base image, diffs it
// against img, and emits added/changed paths as entries and removed paths as
// overlay whiteouts. Applied on top of the base, the layer reproduces img
// exactly — so a round-trip Flatten of the result equals img. Regular files are
// compared by content (hashed), so equal-sized but modified files are detected.
//
// As with AddLayer, the base image's architecture, OS and runtime configuration
// are preserved, and only opt's Ref, Gzip and Created are used. The result is
// deterministic when img is wired with a fixed clock.
func AddLayerDiff(dst *Layout, baseRef string, img *image.Mem, opt BuildOptions) (Descriptor, error) {
	man, cfg, err := dst.resolve(baseRef)
	if err != nil {
		return Descriptor{}, err
	}

	oldMem, _, cleanup, err := Flatten(dst, baseRef, img.Deps())
	if err != nil {
		return Descriptor{}, err
	}
	defer cleanup()

	created := opt.Created
	if created.IsZero() {
		created = img.Deps().Clock.Now()
	}
	createdStr := created.UTC().Format(time.RFC3339)

	layerDesc, diffID, err := writeDiffLayer(dst, oldMem.RootNode(), img.RootNode(), opt.Gzip)
	if err != nil {
		return Descriptor{}, err
	}

	cfg.Created = createdStr
	cfg.RootFS.DiffIDs = append(cfg.RootFS.DiffIDs, diffID)
	cfg.History = append(cfg.History, History{Created: createdStr, CreatedBy: "fsforge"})

	ref := opt.Ref
	if ref == "" {
		ref = baseRef
	}
	return writeImageMeta(dst, append(man.Layers, layerDesc), cfg, ref)
}
