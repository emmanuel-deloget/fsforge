package main

import (
	"flag"
	"fmt"

	fsforge "github.com/emmanuel-deloget/fsforge"
)

func mkfs(args []string) error {
	fsSet := flag.NewFlagSet("mkfs", flag.ContinueOnError)
	typ := fsSet.String("type", "", "filesystem type: ext2, ext4, fat, exfat, iso, squashfs, erofs, cpio, udf, cramfs, romfs")
	source := fsSet.String("source", "", "source directory")
	output := fsSet.String("output", "", "output image file")
	sizeStr := fsSet.String("size", "", "image size (fixed-size types), e.g. 64M")
	blockSize := fsSet.Uint("block-size", 0, "block size in bytes")
	label := fsSet.String("label", "", "volume label")
	reproducible := fsSet.Bool("reproducible", false, "deterministic output")
	if err := fsSet.Parse(args); err != nil {
		return err
	}
	if *typ == "" || *source == "" || *output == "" {
		return fmt.Errorf("-type, -source and -output are required")
	}

	b := fsforge.New(*typ).
		Size(*sizeStr).
		BlockSize(uint32(*blockSize)).
		Label(*label)
	if *reproducible {
		b.Reproducible(fsforge.SourceDateEpoch())
	}
	if err := b.BuildFromDir(*source, *output); err != nil {
		return err
	}
	fmt.Printf("wrote %s image %s\n", *typ, *output)
	return nil
}
