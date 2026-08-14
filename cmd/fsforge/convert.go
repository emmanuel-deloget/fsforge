package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/pkg/ociremote"
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
	platform := fsSet.String("platform", "", "platform to take from a multi-platform image, e.g. linux/arm64")
	insecure := fsSet.Bool("registry-insecure", false, "allow plain HTTP to the registry")
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
	opt.Registry = ociremote.Options{
		Platform: *platform,
		Insecure: *insecure,
		// Credentials come from the environment rather than the command line,
		// where they would land in shell history and process listings.
		Username: os.Getenv("FSFORGE_REGISTRY_USER"),
		Password: os.Getenv("FSFORGE_REGISTRY_PASSWORD"),
	}
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
	// A registry reference is written the way every other tool writes it, and
	// its "docker://" is a scheme rather than a kind:path separator.
	if rest, ok := strings.CutPrefix(s, "docker://"); ok {
		return fsforge.Location{Kind: "docker", Path: rest}, nil
	}
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return fsforge.Location{}, fmt.Errorf("expected <kind>:<path>, got %q", s)
	}
	return fsforge.Location{Kind: s[:i], Path: s[i+1:]}, nil
}
