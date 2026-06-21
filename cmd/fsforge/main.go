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
	case "convert":
		err = convert(os.Args[2:])
	case "disk":
		err = disk(os.Args[2:])
	case "oci-add-layer":
		err = ociAddLayer(os.Args[2:])
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
  fsforge mkfs -type <ext2|ext4|fat|exfat|iso|squashfs|erofs|cpio|udf> -source <dir> -output <file> [options]
  fsforge convert -from <kind>:<path> -to <kind>:<path> [options]
  fsforge disk -output <file> -size <size> -part <role>:<fstype>:<source>:<size> ...
  fsforge oci-add-layer -image <oci-dir> -from <dir|kind:path> [-ref <tag>] [-diff]

mkfs options:
  -type         filesystem type (ext2, ext4, squashfs)        [required]
  -source       directory whose contents populate the image   [required]
  -output       output image file                             [required]
  -size         image size for ext (e.g. 64M, 512M, 1G)       [required for ext]
  -block-size   block size in bytes (engine default if unset)
  -label        volume label
  -reproducible deterministic output (fixed timestamps and UUID)

convert: <kind> is dir, ext2, ext4, squashfs, exfat, iso, erofs, cpio, udf or oci.
  -from <kind>:<path>   source  (dir, ext2, ext4, squashfs, exfat, iso, erofs, cpio, udf, oci)
  -to   <kind>:<path>   sink    (dir, ext2, ext4, squashfs, exfat, iso, erofs, cpio, udf, oci)
  -size, -block-size    as for mkfs (ext sinks need -size)
  -ref                  image ref for an oci sink (default fsforge:latest)
  -reproducible         deterministic output

  e.g. fsforge convert -from oci:./alpine-oci -to ext4:rootfs.img -size 256M
       fsforge convert -from dir:./rootfs    -to oci:./image-oci -ref app:v1

disk: a GPT disk with one or more engine-formatted partitions.
  -output <file>   output disk image; a .qcow2/.qcow path emits QCOW2 [required]
  -size <size>     total disk size (e.g. 512M, 2G)             [required]
  -part R:F:S:Z    role R (esp|root|data), fstype F (fat|ext2|ext4),
                   source dir S, size Z (e.g. 64M or 'rest'); repeatable
  -reproducible    deterministic output

  e.g. fsforge disk -output disk.img -size 512M \
         -part esp:fat:./esp:64M -part root:ext4:./rootfs:rest
       fsforge disk -output vm.qcow2 -size 2G -part root:ext4:./rootfs:rest

Any output path ending in .qcow2/.qcow (for mkfs, convert sinks or disk) writes
a sparse QCOW2 container; QCOW2 inputs are decoded transparently.

oci-add-layer: stack another layer onto an existing OCI image layout.
  -image <oci-dir> OCI layout directory                         [required]
  -from <src>      new layer source: a directory or <kind>:<path> [required]
  -ref <tag>       image ref/tag to extend (default first manifest)
  -diff            append a delta layer (whiteouts removals) vs additive
  -gzip            gzip-compress the layer (default true)
  -reproducible    deterministic output

  e.g. fsforge oci-add-layer -image ./image-oci -ref app:v1 -from ./patch
       fsforge oci-add-layer -image ./image-oci -from dir:./newroot -diff
`)
}
