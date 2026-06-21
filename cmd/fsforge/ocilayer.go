package main

import (
	"flag"
	"fmt"
	"strings"

	fsforge "github.com/emmanuel-deloget/fsforge"
)

// ociAddLayer appends a layer to an existing OCI image layout.
func ociAddLayer(args []string) error {
	fsSet := flag.NewFlagSet("oci-add-layer", flag.ContinueOnError)
	layout := fsSet.String("image", "", "OCI image layout directory")
	ref := fsSet.String("ref", "", "image ref/tag to extend (default: first manifest)")
	from := fsSet.String("from", "", "new layer source: a directory, or <kind>:<path> (dir, ext2, ext4, squashfs, oci)")
	diff := fsSet.Bool("diff", false, "append a delta layer (whiteouts removals) instead of an additive one")
	gzip := fsSet.Bool("gzip", true, "gzip-compress the layer")
	reproducible := fsSet.Bool("reproducible", false, "deterministic output")
	if err := fsSet.Parse(args); err != nil {
		return err
	}
	if *layout == "" || *from == "" {
		return fmt.Errorf("-image and -from are required")
	}

	src := fsforge.Location{Kind: "dir", Path: *from}
	if strings.IndexByte(*from, ':') >= 0 {
		loc, err := parseLoc(*from)
		if err != nil {
			return err
		}
		src = loc
	}

	opt := fsforge.OCILayerOptions{Gzip: *gzip, Diff: *diff}
	if *reproducible {
		opt.Deps = fsforge.ReproducibleDeps(fsforge.SourceDateEpoch())
	}
	if err := fsforge.AddOCILayer(*layout, *ref, src, opt); err != nil {
		return err
	}

	kind := "additive"
	if *diff {
		kind = "diff"
	}
	fmt.Printf("appended %s layer from %s to %s\n", kind, *from, *layout)
	return nil
}
