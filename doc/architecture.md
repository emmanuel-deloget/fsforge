# fsforge — Architecture & Intent

> Status: design intent. This document is the source of truth for *why* the code
> is shaped the way it is. Code is expected to follow it; when they disagree,
> fix one of them deliberately.

## 1. Purpose

fsforge is a **pure-Go library for building and offline-mutating filesystem
images**, with a thin CLI on top. It exists because the Go ecosystem has no
dependable, privilege-free, cgo-free way to *produce* valid filesystem images —
the existing libraries are read-oriented or have partial, fragile write paths
(notably for ext4).

The driving use case is **fully controlled, reproducible image generation**: OS
and appliance images, container/embedded rootfs, VM disks, and reproducible
build artifacts — created in unprivileged CI, on any host OS.

## 2. Goals

- **Create** filesystem images from a described directory tree.
- **Mutate** existing images **offline** (never while mounted).
- Produce images the **official tools accept** (`fsck` clean, mountable).
- Be **reproducible by construction**: identical inputs ⇒ byte-identical output.
- Support, over time, **as many filesystems as can be written correctly**.

## 3. Non-goals

- **No read-only filesystems.** If we cannot *write* a format, we do not ship
  it. Parsing alone provides no value for this project.
- **No online/mounted operation.** No live mutation, no crash-consistency
  guarantees *during* an operation.
- **No cgo, no shelling out, no root.** The library is pure Go and self-contained.
  External tools appear only in the test harness.
- **No nightmare formats** (btrfs, ZFS) until/unless a correct *writer* is
  realistic. NTFS is out for the same reason.

## 4. Guiding principles

### 4.1 Pure Go, no privileges
The library links no C and invokes no external process. Everything works as an
ordinary user, on Linux/macOS/Windows.

### 4.2 Offline only — the great simplifier
Because images are never mounted while we touch them, the hardest parts of a
filesystem writer disappear:
- no crash-consistency to maintain mid-operation;
- no correct incremental allocation — we may repack/defragment freely;
- **journals become trivial**: on load we *replay* an existing journal to reach
  a consistent state, then on finalize we write a *fresh empty* journal. We
  never implement transactional recovery.

### 4.3 Write or nothing
Every supported filesystem is a *write target*. Read support exists only insofar
as it serves writing (loading an image to mutate it, or validating round-trips).

### 4.4 Dependency injection everywhere
Anything that is environment or policy is received through an **interface**,
never a concrete type. This keeps the engines deterministic and unit-testable
against in-memory fakes. The boundary is explicit:

- **Injected** (environment & policy): block IO (`device.Device`), time
  (`image.Clock`), identifiers (`image.UUIDSource`), block allocation
  (`alloc.Factory`), compression (`compress.Compressor`), logging.
- **Not injected** (format-mandated correctness): on-disk struct encoding,
  `crc32c`, the `half_md4` htree hash. Hiding these behind interfaces buys
  nothing and adds risk; they are unexported and covered by golden vectors.

### 4.5 Reproducibility falls out of wiring
There is **no `reproducible` flag inside engines**. A reproducible build is
simply one wired with `FixedClock` + `FixedUUID` + the deterministic bitmap
allocator. A host build swaps in `SystemClock` + `RandomUUID`. The engines are
identical in both cases. This is the main payoff of §4.4.

## 5. The unifying model: create == offline-mutate

Create and mutate are **one pipeline**, not two engines:

```
[load]  ──►  logical tree (lazy)  ──►  [mutate]  ──►  [deterministic layout]  ──►  bytes
```

- For **create**, the `load` step starts from an empty tree.
- For **mutate**, `load` lazily parses the existing image into the same tree.
- File contents are **never** buffered in full: inodes reference a `tree.Source`
  (`io.ReaderAt` + `Size`) that is streamed during layout. Only the metadata
  tree lives in memory, so image size is not bounded by RAM.
- `Finalize` runs the engine's layout pass, which is a pure function of
  `(tree, allocator, environment)` and therefore deterministic.

## 6. Layered architecture

```
L4  Public API      image: Image / Dir / File / Filesystem / Deps          (pkg/image)
L3  Logical model   tree:  Inode / Dirent / Meta / Source                  (pkg/tree)
L2  Engines         ext2/3/4, squashfs, exfat, fat, iso9660, …             (pkg/<fs>)
L1  Container       MBR / GPT partition tables                             (planned)
L0  Block backend   device: Device / Discarder, Mem / File / Section       (pkg/device)
        ┌── policy injected into engines ──┐
        alloc (allocation)  ·  compress (codecs)  ·  image.Clock/UUID (env)
```

Dependency direction is strictly downward; `pkg/device` and `pkg/tree` depend on
nothing else in the module, which keeps the graph acyclic and fully mockable.

## 7. Project layout

Follows the conventions of `golang-standards/project-layout`.

| Path                     | Role                                                              |
|--------------------------|------------------------------------------------------------------|
| `cmd/fsforge/`           | The CLI binary; a thin shell over the library, no business logic.|
| `pkg/device/`            | Block-device abstraction + `Mem`/`File`/`Section` backends.       |
| `pkg/tree/`              | Filesystem-agnostic logical tree (inodes, dirents, sources).     |
| `pkg/image/`             | Public contracts (`Image`/`Dir`/`Filesystem`) + injected `Deps`. |
| `pkg/alloc/`             | `Allocator` interface + deterministic bitmap implementation.     |
| `pkg/compress/`          | `Compressor` interface, registry, pure-Go codec adapters.        |
| `pkg/ext/`               | ext2/3/4 engine.                                                  |
| `pkg/squashfs/`          | squashfs engine.                                                  |
| `pkg/fat/`               | FAT32 engine (ESP/boot/data volumes).                             |
| `pkg/iso/`               | ISO9660 + Rock Ridge engine (CD/DVD images).                      |
| `pkg/partition/`         | GPT partition tables; carves a disk into device.Sections.         |
| `pkg/oci/`               | OCI image read (flatten) and write (build); tree as the hub.      |
| `internal/binio/`        | Module-private checksum/binary helpers.                           |
| `internal/conformance/`  | Privileged, build-tagged test harness (official-tool validation).|
| `doc/`                   | This document and design notes.                                   |

Format-mandated encoders/hashes stay **unexported inside each engine package**
rather than in `internal/`, so they sit next to the code that defines them and
are tested with golden vectors.

## 8. Core contracts

The shape (see `pkg/image`, `pkg/tree`, `pkg/alloc`):

- `image.Filesystem`: `Format(dev, params)` and `Open(dev)` — the two entry
  points, returning the **same** editable `Image`.
- `image.Image`: `Root() Dir`, `Finalize()`.
- `image.Dir`: `Mkdir/Create/Symlink/Mknod/Link/Lookup/Remove/Range` — the one
  editing surface, shared by created and loaded images.
- `image.Deps`: the injected `Clock`, `UUIDSource`, `alloc.Factory`, `Logger`.
- `tree.Source`: lazy file contents (`io.ReaderAt` + `Size`).
- `alloc.Allocator`: contiguous block runs; the bitmap impl is deterministic.

## 9. Testing strategy

Correctness *is* the product; the harness is not an afterthought. Three
in-process levels plus an external one:

1. **Structure** — pure encode/decode round-trips for each on-disk structure,
   against golden byte vectors.
2. **Layout** — run an engine's layout against a `device.Mem` with the
   deterministic allocator; assert on the produced bytes (golden images) and on
   reproducibility (two runs ⇒ identical bytes).
3. **Engine** — create/mutate via the public API, read back via our own parser,
   compare the logical tree.
4. **Conformance** (`internal/conformance`, privileged CI, build-tagged):
   `fsck`/mount/read-back, **differential** comparison vs the reference tool
   (structural, not byte-for-byte), and **cross round-trips** (we-write/tool-read
   and tool-write/we-read/we-mutate/tool-validate). Plus property/fuzz: random
   tree ⇒ finalize ⇒ mount ⇒ read back ⇒ diff.

## 10. Supported filesystems & roadmap

All entries are **write targets**. Ordering is by value/effort and by the fact
that ext2 validates the whole architecture before ext4's complexity.

| Milestone | Status | Scope                                                          |
|-----------|--------|----------------------------------------------------------------|
| **M1**    | ✅ done | **ext2** create: full layout, indirect blocks, round-trip reader. |
| **M2**    | ✅ done | **ext4** create via an extents variant (inline extent tree, 256-byte inodes, FILETYPE+EXTENTS). 64bit/metadata_csum/htree/flex_bg and extent-tree index nodes remain. |
| **M4**    | ✅ done | **squashfs** 4.0 create (non-fragmented, zlib), validated against `unsquashfs`. |
| **M3**    | ✅ done | **ext2/4** offline mutation via staged re-layout (scratch device avoids read-before-overwrite); mutated images pass e2fsck. |
| **OCI**   | ✅ done | Read (flatten layers+whiteouts → tree) and write (tree → tar layer + config/manifest/index) of OCI image layouts. Validated by podman: it pulls fsforge-built images, and fsforge flattens real `podman save` output. |
| **convert** | ✅ done | `fsforge convert` bridges any source to any sink through the tree: dir/ext2/ext4/oci → dir/ext2/ext4/squashfs/oci. `oci→ext4` of real alpine passes e2fsck. |
| **CLI**   | ✅ done | `fsforge mkfs` builds ext2/ext4/squashfs from a directory; `fsforge convert` converts between formats. Reproducible. |
| **M0/GPT** | ✅ done | GPT partition tables (protective MBR + primary/backup headers + entry array, CRC32, deterministic GUIDs). `fsforge disk` builds a full GPT disk (ESP FAT32 + ext4 root); validated by sfdisk and per-partition fsck. MBR-only tables remain. |
| **M6a**   | ✅ done | **FAT12/16/32** create (auto type by size or forced; LFN + generated 8.3, fixed root for 12/16, FSInfo for 32), all validated by `fsck.fat`. Wired into mkfs and convert. |
| **M6b**   | ✅ done | **ISO9660 + Rock Ridge** create (POSIX names/perms, symlinks, devices; single-extent files), validated by `xorriso` extract. `oci→iso` of real alpine round-trips. Wired into mkfs and convert. |
| **MBR**   | ✅ done | MBR partition tables (up to 4 primaries, bootable flag), validated by sfdisk; `disk -scheme mbr`. |
| **M5**    | todo   | **exFAT** create + mutate.                                     |
| **M6c**   | todo   | ISO9660 deep-relocation/CE for very long names; squashfs reader. |
| later     | todo   | **erofs** / **UDF** if demand warrants. NTFS/btrfs/ZFS: out of scope until a correct *writer* is realistic. |

> Conformance: ext2 and ext4 images pass `e2fsck -fn` cleanly (e2fsprogs 1.47.4),
> run via `internal/conformance` either from a host binary or a container
> runtime (`go test -tags conformance ./pkg/ext/`). squashfs is validated
> end-to-end by `unsquashfs`. The round-trip reader remains the fast in-process
> check. Loopback-mount round-trips are still a CI add-on.

## 11. References

- ext4 disk layout — kernel docs `Documentation/filesystems/ext4/`.
- squashfs format — kernel docs / `squashfs-tools`.
- exFAT specification — Microsoft (opened, 2019).
- Reproducible builds — `SOURCE_DATE_EPOCH` convention.
