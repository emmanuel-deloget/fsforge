package fsforge

import (
	"fmt"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/oci"
)

// OCILayerOptions configures appending a layer to an existing OCI image.
type OCILayerOptions struct {
	// Deps selects reproducible vs host behaviour. A zero Deps defaults to
	// HostDeps.
	Deps image.Deps
	// Gzip gzip-compresses the new layer (recommended).
	Gzip bool
	// Diff appends a delta layer (added/changed paths plus whiteouts for
	// removals) instead of an additive one. With Diff, src is the desired end
	// state; without it, src is unioned over the existing layers.
	Diff bool
}

// AddOCILayer appends the tree at src as a new layer on top of the image tagged
// baseRef in the OCI layout at layoutPath. src may be any supported conversion
// source (dir, ext2/ext4, squashfs, oci): it is loaded into a tree and stacked
// as a layer.
//
// By default the layer is additive (a union over the lower layers). Set
// OCILayerOptions.Diff to record a delta instead, which also removes paths the
// base image had but src does not.
func AddOCILayer(layoutPath, baseRef string, src Location, opt OCILayerOptions) error {
	if opt.Deps.Clock == nil {
		opt.Deps = HostDeps()
	}

	root, _, cleanup, err := loadTree(src.Kind, src.Path, opt.Deps)
	if err != nil {
		return fmt.Errorf("load %s:%s: %w", src.Kind, src.Path, err)
	}
	defer cleanup()

	l, err := oci.OpenLayout(layoutPath)
	if err != nil {
		return err
	}
	mem := image.Adopt(opt.Deps, root)
	bo := oci.BuildOptions{Ref: baseRef, Gzip: opt.Gzip}

	if opt.Diff {
		_, err = oci.AddLayerDiff(l, baseRef, mem, bo)
	} else {
		_, err = oci.AddLayer(l, baseRef, mem, bo)
	}
	return err
}
