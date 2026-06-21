package main

import (
	"flag"
	"fmt"
	"strings"

	fsforge "github.com/emmanuel-deloget/fsforge"
)

// convert wires any supported source to any supported sink through the shared
// tree model, delegating entirely to the fsforge package.
func convert(args []string) error {
	fsSet := flag.NewFlagSet("convert", flag.ContinueOnError)
	from := fsSet.String("from", "", "source as <kind>:<path> (dir, ext2, ext4, squashfs, exfat, iso, oci)")
	to := fsSet.String("to", "", "sink as <kind>:<path> (dir, ext2, ext4, squashfs, fat, exfat, iso, oci)")
	sizeStr := fsSet.String("size", "", "image size for fixed-size sinks, e.g. 512M")
	blockSize := fsSet.Uint("block-size", 0, "block size in bytes")
	ref := fsSet.String("ref", "fsforge:latest", "image ref for oci sink")
	reproducible := fsSet.Bool("reproducible", false, "deterministic output")
	if err := fsSet.Parse(args); err != nil {
		return err
	}
	if *from == "" || *to == "" {
		return fmt.Errorf("-from and -to are required")
	}

	src, err := parseLoc(*from)
	if err != nil {
		return err
	}
	dst, err := parseLoc(*to)
	if err != nil {
		return err
	}

	opt := fsforge.Options{Size: *sizeStr, BlockSize: uint32(*blockSize), Ref: *ref}
	if *reproducible {
		opt.Deps = fsforge.ReproducibleDeps(fsforge.SourceDateEpoch())
	}
	if err := fsforge.Convert(src, dst, opt); err != nil {
		return err
	}
	fmt.Printf("converted %s -> %s\n", *from, *to)
	return nil
}

func parseLoc(s string) (fsforge.Location, error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return fsforge.Location{}, fmt.Errorf("expected <kind>:<path>, got %q", s)
	}
	return fsforge.Location{Kind: s[:i], Path: s[i+1:]}, nil
}
