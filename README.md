# fsforge

[![CI](https://github.com/emmanuel-deloget/fsforge/actions/workflows/ci.yml/badge.svg)](https://github.com/emmanuel-deloget/fsforge/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/emmanuel-deloget/fsforge.svg)](https://pkg.go.dev/github.com/emmanuel-deloget/fsforge)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Build filesystem images in pure Go — no root, no cgo, no shelling out, and
reproducible by construction.**

fsforge turns a directory (or an OCI image, or another filesystem image) into a
valid, mountable filesystem image, entirely in-process. It targets the case Go
has long lacked: producing real filesystem images in unprivileged CI, on any
host OS — OS/appliance images, container and embedded rootfs, VM disks, and
reproducible build artifacts.

## Supported formats

Every format is a **write target** (fsforge creates it; some it can also read
back to convert or mutate).

| Format            | Create | Load / convert source | Validated with     |
|-------------------|:------:|:---------------------:|--------------------|
| ext2 / ext3 / ext4| ✅     | ✅                    | `e2fsck`           |
| squashfs          | ✅     | ✅                    | `unsquashfs`       |
| FAT12 / 16 / 32   | ✅     | —                     | `fsck.fat`         |
| exFAT             | ✅     | —                     | `fsck.exfat`       |
| ISO9660 + Rock Ridge | ✅  | —                     | `xorriso`          |
| OCI image layout  | ✅     | ✅ (flatten)          | `podman`           |
| GPT / MBR disks   | ✅     | —                     | `sfdisk` + per-part `fsck` |

## Install

Library:

```bash
go get github.com/emmanuel-deloget/fsforge
```

CLI:

```bash
go install github.com/emmanuel-deloget/fsforge/cmd/fsforge@latest
```

## Quickstart — library

Build a reproducible ext4 image from a directory:

```go
package main

import (
	"log"

	fsforge "github.com/emmanuel-deloget/fsforge"
)

func main() {
	err := fsforge.New("ext4").
		Reproducible(fsforge.SourceDateEpoch()). // honour SOURCE_DATE_EPOCH
		Size("256M").
		Label("root").
		BuildFromDir("./rootfs", "root.img")
	if err != nil {
		log.Fatal(err)
	}
}
```

Convert between formats through the shared tree model:

```go
// An OCI image directory into an ext4 root filesystem.
err := fsforge.Convert(
	fsforge.Location{Kind: "oci", Path: "./alpine-oci"},
	fsforge.Location{Kind: "ext4", Path: "rootfs.img"},
	fsforge.Options{Size: "256M"},
)
```

Need finer control? The same building blocks are exported: `EngineFor` selects
an engine, `PopulateFromDir` / `Graft` fill an image tree, `ExtractToDir` writes
one back out, and `HostDeps` / `ReproducibleDeps` choose the injected clock and
UUID source. See the [package reference](https://pkg.go.dev/github.com/emmanuel-deloget/fsforge).

## Quickstart — CLI

```bash
# Make an ext4 image from a directory.
fsforge mkfs -type ext4 -source ./rootfs -output root.img -size 256M

# Convert an OCI image to a squashfs archive.
fsforge convert -from oci:./alpine-oci -to squashfs:rootfs.sqfs

# Build a bootable GPT disk: an ESP (FAT32) plus an ext4 root.
fsforge disk -output disk.img -size 512M \
  -part esp:fat:./esp:64M -part root:ext4:./rootfs:rest

# Reproducible output: fixed timestamps and UUID.
SOURCE_DATE_EPOCH=0 fsforge mkfs -type ext4 -source ./rootfs \
  -output root.img -size 256M -reproducible
```

Run `fsforge help` for the full flag reference.

## Reproducibility

Identical inputs produce **byte-identical** output. There is no special mode:
a reproducible build is just one wired with a fixed clock and UUID (via
`Reproducible` / the `-reproducible` flag), so the same tree always lays out the
same bytes. This is ideal for content-addressed artifacts and supply-chain
verification.

## How it works

fsforge models every filesystem as one logical tree of inodes, then lets each
engine lay that tree out on disk deterministically. File contents are streamed
at finalize time, never buffered in full, so image size is not bounded by RAM.
Environment and policy (block IO, clock, identifiers, allocation, compression)
are all injected, which is what makes engines deterministic and testable.

For the full design, see **[doc/architecture.md](doc/architecture.md)**.

## Build & test

```bash
go build ./...
go test ./...                          # pure-Go, unprivileged
go test -tags conformance ./pkg/ext/   # validates with e2fsck (host or container)
```

## License

[MIT](LICENSE) © Emmanuel Deloget
