# fsforge

<img src="assets/icon.svg" alt="" width="88" align="right">

[![CI](https://github.com/emmanuel-deloget/fsforge/actions/workflows/ci.yml/badge.svg)](https://github.com/emmanuel-deloget/fsforge/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/emmanuel-deloget/fsforge.svg)](https://pkg.go.dev/github.com/emmanuel-deloget/fsforge)
[![Release](https://img.shields.io/github/v/release/emmanuel-deloget/fsforge)](https://github.com/emmanuel-deloget/fsforge/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**[emmanueldeloget.com/fsforge](https://www.emmanueldeloget.com/fsforge/)** —
what it builds, in fewer words than this page.

**Build filesystem images in pure Go — zero dependencies, no root, no cgo, no
shelling out, and reproducible by construction.**

fsforge turns a directory (or an OCI image, or another filesystem image) into a
valid, mountable filesystem image, entirely in-process. It targets the case Go
has long lacked: producing real filesystem images in unprivileged CI, on any
host OS — OS/appliance images, container and embedded rootfs, VM disks, and
reproducible build artifacts.

## Memory does not follow image size

![Memory held and bytes written while building a 4 GiB ext4 image: memory rises
to 44 MiB while the tree is built, then stays flat as 3.4 GiB are
written](doc/profile.svg)

Contents flow through `tree.Source` and are streamed at finalize, so what fsforge
holds is the metadata tree and nothing else — roughly a kilobyte per file. The
plateau above does not move while the bytes written triple.

Which is why the two figures below are both true and neither is the whole story:

| Source | Held |
|---|---:|
| one 2 GiB file | 400 KiB |
| 50 000 files, 3.4 GiB | 44 MiB |

Memory tracks the *number* of files, not their volume. A single enormous file
costs almost nothing; a kernel tree costs a kilobyte an entry whatever its size.
`TestStreamingKeepsMemoryBounded` pins the first case and fails if streaming
stops.

Draw it on your own machine, corpus and all:

```bash
go run ./internal/profile -svg doc/profile.svg
```

It generates the source tree the first time (a few minutes, kept for later
runs), measures, and writes the chart.

Nothing in fsforge is parallel, and it does not need to be: the same build takes
3.8 s on one core and 4.1 s on twelve, the difference being scheduling noise. A
one-core CI runner is not a handicap.

## Speed

A 66 MiB tree of 2 000 files with a rootfs-like size distribution, on an
i7-8850H, `-benchtime 3x`:

| Engine   | Throughput | Image / input | Allocated |
|----------|-----------:|--------------:|----------:|
| ISO 9660 |  588 MB/s  |         1.02× |    105 MB |
| ext4     |  500 MB/s  |         1.99× |    109 MB |
| cpio     |  491 MB/s  |         1.00× |    135 MB |
| EROFS    |  467 MB/s  |         1.05× |     13 MB |
| squashfs |   49 MB/s  |         0.18× |    360 MB |

squashfs is an order of magnitude slower because it is the only one compressing
— that is also why its output is a fifth of the input. Reproduce with
`go test -run '^$' -bench . -benchmem .`

## Supported formats

Every format is a **write target** (fsforge creates it; some it can also read
back to convert or mutate).

| Format            | Create | Load / convert source | Validated with     |
|-------------------|:------:|:---------------------:|--------------------|
| ext2 / ext3 / ext4| ✅     | ✅                    | `e2fsck`           |
| squashfs          | ✅     | ✅                    | `unsquashfs`       |
| EROFS             | ✅     | ✅                    | `fsck.erofs`       |
| cpio newc (initramfs) | ✅ | ✅                    | `cpio`             |
| UDF 2.01          | ✅     | ✅                    | kernel mount, `udfinfo`, `7z` |
| cramfs            | ✅     | ✅                    | `7z`               |
| romfs             | ✅     | ✅                    | `genromfs`         |
| FAT12 / 16 / 32   | ✅     | —                     | `fsck.fat`         |
| exFAT             | ✅     | ✅                    | `fsck.exfat`       |
| ISO9660 + Rock Ridge | ✅  | ✅                    | `xorriso`          |
| OCI image layout  | ✅     | ✅ (flatten)          | `podman`           |
| GPT / MBR disks   | ✅     | —                     | `sfdisk` + per-part `fsck` |
| QCOW2 container   | ✅     | ✅                    | `qemu-img`         |

QCOW2 is a disk-image *container*, not a filesystem: it wraps any of the above.
Give an output path ending in `.qcow2` to `mkfs`, a `convert` sink, or
`fsforge disk` and the result is a sparse QCOW2 (e.g. a bootable VM disk);
QCOW2 inputs are decoded transparently.

## Install

Library:

```bash
go get github.com/emmanuel-deloget/fsforge
```

CLI:

```bash
go install github.com/emmanuel-deloget/fsforge/cmd/fsforge@latest
```

fsforge has **zero third-party dependencies**: it imports only the Go standard
library, so its `go.mod` carries no `require` block and there is no `go.sum` —
nothing transitive to vendor, audit or keep up to date. The external tools in
the table above are used **only by the conformance tests**, never by the library
or CLI at runtime.

## In a GitHub workflow

```yaml
- uses: emmanuel-deloget/fsforge@v1
  with:
    type: ext4
    source: ./rootfs
    output: rootfs.img
    size: 256M
    spec: rootfs.mtree      # ownership and device nodes
```

No privileged container, no loop device, no `sudo`. The action outputs the
image's path, size and `sha256`, so a later step can attest or publish it
without rehashing.

## From a registry, in one step

```bash
fsforge convert -from docker://alpine:3.20 -to ext4:rootfs.img -size 256M
```

The image is pulled straight from the registry — no `docker pull`, no daemon, no
local layout to prepare — flattened, and written as a filesystem. Every blob is
checked against the digest that named it before it is used.

```go
ref, err := ociremote.Pull("ghcr.io/owner/app:v1", "./app-oci", ociremote.Options{
    Platform: "linux/arm64",
})
```

`pkg/ociremote` is the only package that opens a socket; a build that never
mentions a registry never touches the network.

## Ownership, modes and device nodes, without root

A checkout is owned by whoever cloned it, holds no device nodes, and loses
setuid bits on most CI filesystems. State those separately, in mtree(5) — the
format BSD, Yocto and Buildroot already use:

```bash
fsforge spec -source ./rootfs -output rootfs.mtree   # describe what is there
$EDITOR rootfs.mtree                                 # say what the image needs
fsforge mkfs -type ext4 -source ./rootfs -spec rootfs.mtree \
  -size 256M -output rootfs.img
```

```mtree
/set uid=0 gid=0
./bin/ping     type=file mode=4755
./dev/console  type=char mode=0600 device=native,5,1
./tmp          type=dir  mode=01777
./var/run      type=link link=../run
```

The files come from the directory; the facts about them come from a file you
keep in the repository. Nothing in that build needs privileges.

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

# Stack another layer onto an existing OCI image (additive, or -diff for a delta).
fsforge oci-add-layer -image ./image-oci -ref app:v1 -from ./patch

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

## Contributing

Bug reports and patches are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for
the rules that are not negotiable (pure Go, no dependencies, write-or-nothing)
and for what a good test looks like here. Security reports have their own route:
[SECURITY.md](SECURITY.md).

What changed and when is in [CHANGELOG.md](CHANGELOG.md).

## License

[MIT](LICENSE) © Emmanuel Deloget
