# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) —
with the caveat that it is pre-1.0, so a minor version may break the API. What
will not break silently is an image: a change to what fsforge writes is called
out under **Changed** or **Fixed**, with what it means for images already built.

## [Unreleased]

### Added

- **`internal/profile`**, which measures what a build costs over time — memory
  held and bytes written, on one clock — and draws the chart the README shows.
  It generates its own corpus, so a reader who doubts the numbers runs one
  command and gets their own. The memory curve reads `/gc/heap/live:bytes`
  rather than `HeapAlloc`: the latter counts garbage not yet collected, and its
  sawtooth says when the collector ran, not what the program holds.

## [0.3.0] — 2026-08-14

### Upgrading

No API breaks: everything added is additive, and code written against 0.2.0
compiles unchanged. Images, however, are not byte-identical to what 0.2.0
produced from the same inputs — extended attributes are now stored, squashfs
uses extended inodes for nodes that carry them, and several timestamp and
encoding bugs are fixed. A build pipeline that pins an image digest will see it
change once, at this upgrade.

### Added

- **Extended attributes** are written and read back by ext2/ext4, squashfs and
  EROFS. `tree.Meta.Xattrs` had been part of the model from the start and the
  OCI flatten filled it from a layer's PAX records, but no engine stored it:
  converting an OCI image to ext4 silently dropped `security.capability` and
  `security.selinux`, which is the difference between a rootfs that boots
  enforcing and one that does not.
- **`pkg/mtree`** reads and writes mtree(5), so a build can state the ownership,
  modes and device nodes an unprivileged checkout cannot hold. `Builder.Spec`
  applies one between populate and finalize; `fsforge spec` writes one out for a
  directory. This is what makes "build a rootfs without root" complete rather
  than half-done.
- **`pkg/ociremote`** pulls images straight from a registry —
  `fsforge convert -from docker://alpine:3.20 -to ext4:rootfs.img` — with no
  daemon and no local layout to prepare. Blobs are verified against the digest
  that named them. It is the only package that opens a socket.
- **A GitHub Action** (`action.yml`), so building an image in unprivileged CI is
  three lines of YAML. It reports the image's path, size and sha256.
- **Benchmarks** (`go test -bench .`) and a streaming test that measures: writing
  a 2 GiB image peaks at 400 KiB of heap, and `TestStreamingKeepsMemoryBounded`
  fails if that stops being true.
- A **differential test harness** — `internal/fsgen` generates trees from a seed,
  `internal/manifest` flattens and diffs them field by field. Three levels run:
  in-process round trips over ten engines, extraction by each format's own tool,
  and ingestion of an image that tool built.
- `SECURITY.md`, `CONTRIBUTING.md` and this file; `govulncheck`, a `gofmt` gate
  and a weekly scheduled run in CI.
- `fsforge version`, reporting the module version, platform and toolchain.

### Fixed

Several of these were found by the differential tests and could not have been
found without them: a writer and a reader that share a misreading of a format
agree with each other perfectly.

- **Path traversal when building a tree from an untrusted image.** Every engine's
  `Open` appended directory entries without validating their names, as did the
  OCI flatten. `path.Clean` leaves a leading `..` in place, so a layer entry
  named `../../etc/passwd` created directories literally called `..`, which then
  walked out of the destination when `ExtractToDir` joined them onto a host
  path. Names are now validated in one place (`image.ValidName`), and extraction
  opens files `O_NOFOLLOW`.
- **ISO 9660: every relative symlink came back with a trailing slash.** The Rock
  Ridge `SL` entry declared a length computed from the path text while writing
  flag-only bytes for `.` and `..`, so the reader took the padding for one more,
  empty, component — `../elsewhere` read back as `../elsewhere/`. Affects every
  ISO fsforge has produced.
- **ISO 9660: images came back owned by root.** The `PX` entry carries mode,
  nlink, uid and gid; only mode was being read.
- **UDF: names outside the Basic Multilingual Plane were silently renamed.** CS0
  encoding truncated each rune to sixteen bits, so U+1F642 was stored as U+F642.
  Now encoded with surrogate pairs.
- **cramfs: a name over 252 bytes produced an unreachable file.** `namelen` is
  six bits of four-byte units; a longer name wrapped to zero and the entry read
  back nameless, with the file still in the image. The writer now refuses it.
- **cpio: hard link counts were inflated.** The reader added to a count each
  header already carried in full — three links read back as five.
- **ext: character devices had an incoherent mode.** The reader set
  `ModeCharDevice` without `ModeDevice`, which names no node kind at all under
  `io/fs`.
- **ext, squashfs: a zero `ModTime` was written as a nonsense date.**
  `tree.Meta` documents the zero value as "resolve from the injected clock";
  these two converted it straight to a `uint32`, putting nodes created by a spec
  in the year 2042.
- **squashfs: the extended-directory reader was one word off**, which nothing
  had caught because no extended inode was ever written.
- **OCI layers served with Docker's media types were read as uncompressed.** The
  OCI type ends in `+gzip` and Docker's own ends in `.tar.gzip`, so the string
  test fed compressed bytes to the tar reader for most images on Docker Hub.
  Compression is now detected from the bytes.

### Changed

- **The minimum Go version is now 1.26**, up from 1.24. Go maintains the two
  most recent releases; 1.24 is no longer one of them, and CI took its toolchain
  from `go.mod`, so the declared minimum was also the version everything was
  built and tested with.
- **squashfs images now use extended inodes for nodes that carry attributes.**
  An image with no extended attributes is byte-identical to before.
- **ext images set `EXT2_FEATURE_COMPAT_EXT_ATTR`** when they carry attributes,
  which e2fsck requires before it will accept an attribute block.
- **`compress.Zlib` pools its writers.** Building a squashfs image allocates
  366 MB where it allocated 2411 MB, and takes 19% less wall clock. Output is
  unchanged.
- `cmd/fsforge`'s `main` is split into `main` and `run`, so the dispatch can be
  tested. No change to the command line.

### Known issues

Both are reproduced by flipping a capability on in the differential test table,
and both need a writer rewrite rather than a small fix:

- **romfs corrupts the tree when given hard links.** Header offsets are keyed by
  node, so several names sharing one node overwrite each other's offset and
  whole subdirectories move.
- **cramfs corrupts data when given hard links.** The format has no shared
  inode; rather than duplicating the contents or refusing, the writer emits an
  image whose shared node reads back as corrupt data.

## [0.2.0] — 2026-07-26

### Added

- Large-file support for ext: files are laid out across several runs and the
  extent tree is indexed when its leaves outgrow the inode.
- Conformance tests for large files, against e2fsprogs.

## [0.1.1] — 2026-06-22

### Fixed

- Packaging and documentation fixes following the first release.

## [0.1.0] — 2026-06-21

### Added

- First release: ext2/ext3/ext4, squashfs, EROFS, cpio, UDF, cramfs, romfs,
  FAT12/16/32, exFAT, ISO 9660 with Rock Ridge, OCI image layout, GPT/MBR disks
  and a QCOW2 container, all in pure Go with no third-party dependencies.
- The `fsforge` CLI: `mkfs`, `convert`, `disk`, `oci-add-layer`.
- Reproducible builds by wiring: a fixed clock, a fixed UUID source and a
  deterministic allocator.

[Unreleased]: https://github.com/emmanuel-deloget/fsforge/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/emmanuel-deloget/fsforge/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/emmanuel-deloget/fsforge/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/emmanuel-deloget/fsforge/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/emmanuel-deloget/fsforge/releases/tag/v0.1.0
