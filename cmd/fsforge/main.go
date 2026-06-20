// Command fsforge is a thin CLI over the fsforge library. Its job is to turn a
// source directory into a filesystem image — reproducibly — without root.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "mkfs":
		err = mkfs(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fsforge:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `fsforge — pure-Go filesystem image builder

usage:
  fsforge mkfs -type <ext2|ext4|squashfs> -source <dir> -output <file> [options]

options:
  -type         filesystem type (ext2, ext4, squashfs)        [required]
  -source       directory whose contents populate the image   [required]
  -output       output image file                             [required]
  -size         image size for ext (e.g. 64M, 512M, 1G)       [required for ext]
  -block-size   block size in bytes (engine default if unset)
  -label        volume label
  -reproducible deterministic output (fixed timestamps and UUID)
`)
}
