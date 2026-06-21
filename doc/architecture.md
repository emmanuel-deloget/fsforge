# fsforge — Architecture

This document describes how fsforge is structured and *why* the code takes the
shape it does. It is the reference for the system's design; it does not cover
process, status or roadmap.

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
- Support **as many filesystems as can be written correctly**.

## 3. Non-goals

- **No read-only filesystems.** If we cannot *write* a format, we do not ship
  it. Parsing alone provides no value for this project.
- **No online/mounted operation.** No live mutation, no crash-consistency
  guarantees *during* an operation.
- **No cgo, no shelling out, no root.** The library is pure Go and
  self-contained. External tools appear only in the test harness.
- **No nightmare formats** (btrfs, ZFS) until a correct *writer* is realistic.
  NTFS is out for the same reason.

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
identical in both cases. This is the main payoff of §4.4, and the facade's
`Reproducible` / `Host` switch is nothing more than this wiring choice.

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
L5  Facade          fsforge: Builder / Convert / EngineFor / Populate     (module root)
L4  Public API      image: Image / Dir / File / Filesystem / Deps         (pkg/image)
L3  Logical model   tree:  Inode / Dirent / Meta / Source                 (pkg/tree)
L2  Engines         ext2/3/4, squashfs, erofs, exfat, fat, iso9660, oci   (pkg/<fs>)
L1  Container       MBR / GPT partition tables                            (pkg/partition)
L0  Block backend   device: Device / Discarder, Mem / File / Section      (pkg/device)
        ┌── policy injected into engines ──┐
        alloc (allocation)  ·  compress (codecs)  ·  image.Clock/UUID (env)
```

Dependency direction is strictly downward. `pkg/device` and `pkg/tree` depend on
nothing else in the module, which keeps the graph acyclic and fully mockable.
The facade (L5) sits above everything and only *wires* the lower layers; it
holds no format logic.

Each engine implements `image.Filesystem` and is a **write target**. The current
engines are ext2/3/4, squashfs, EROFS, FAT12/16/32, exFAT and ISO9660 + Rock
Ridge, with OCI image read/write bridged through the same tree. Engines that can
also *load* an existing image (ext, squashfs, EROFS, exFAT, ISO9660, OCI) double
as conversion sources.

EROFS, like squashfs, is read-only once mounted but is nonetheless a *write
target* in fsforge's sense: the engine produces the image. It writes an
uncompressed variant (4 KiB blocks, 64-byte extended inodes, FLAT_PLAIN data)
that `fsck.erofs` and the kernel accept; its reader additionally understands the
compact inodes and inline tails a default `mkfs.erofs` emits, so a tool-written
image opens as a conversion source.

## 7. Project layout

Follows the conventions of `golang-standards/project-layout`, with one
deliberate exception: the **module root holds the `fsforge` facade package**,
because that is the only directory mapping onto the bare published import path
`github.com/emmanuel-deloget/fsforge`.

| Path                     | Role                                                              |
|--------------------------|------------------------------------------------------------------|
| *module root*            | `package fsforge`: high-level `Builder`/`Convert` + reusable helpers; wires the engines. No format logic. |
| `cmd/fsforge/`           | The CLI binary; a thin shell over the facade, no business logic. |
| `pkg/device/`            | Block-device abstraction + `Mem`/`File`/`Section` backends.       |
| `pkg/tree/`              | Filesystem-agnostic logical tree (inodes, dirents, sources).     |
| `pkg/image/`             | Public contracts (`Image`/`Dir`/`Filesystem`) + injected `Deps`. |
| `pkg/alloc/`             | `Allocator` interface + deterministic bitmap implementation.     |
| `pkg/compress/`          | `Compressor` interface, registry, pure-Go codec adapters.        |
| `pkg/ext/`               | ext2/3/4 engine.                                                 |
| `pkg/squashfs/`          | squashfs engine (writer + reader).                              |
| `pkg/erofs/`             | EROFS engine (uncompressed writer + reader).                    |
| `pkg/fat/`               | FAT12/16/32 engine (ESP/boot/data volumes).                     |
| `pkg/exfat/`             | exFAT engine (large/removable volumes).                          |
| `pkg/iso/`               | ISO9660 + Rock Ridge engine (CD/DVD images).                    |
| `pkg/partition/`         | GPT/MBR partition tables; carves a disk into `device.Section`s.  |
| `pkg/oci/`               | OCI image read (flatten) and write (build); tree as the hub.     |
| `internal/binio/`        | Module-private checksum/binary helpers.                          |
| `internal/conformance/`  | Build-tagged test harness (official-tool validation).            |
| `doc/`                   | This document and design notes.                                  |

Format-mandated encoders/hashes stay **unexported inside each engine package**
rather than in `internal/`, so they sit next to the code that defines them and
are tested with golden vectors.

## 8. Core contracts

The shape (see the module root, `pkg/image`, `pkg/tree`, `pkg/alloc`):

- `fsforge.Builder` / `fsforge.Convert`: the high-level facade. A `Builder`
  carries the filesystem type, injected `Deps`, size and label, and runs the
  create pipeline; `Convert` bridges a source `Location` to a sink `Location`.
- `image.Filesystem`: `Format(dev, params)` and `Open(dev)` — the two entry
  points, returning the **same** editable `Image`.
- `image.Image`: `Root() Dir`, `Finalize()`.
- `image.Dir`: `Mkdir/Create/Symlink/Mknod/Link/Lookup/Remove/Range` — the one
  editing surface, shared by created and loaded images.
- `image.Deps`: the injected `Clock`, `UUIDSource`, `alloc.Factory`, `Logger`.
- `tree.Source`: lazy file contents (`io.ReaderAt` + `Size`).
- `alloc.Allocator`: contiguous block runs; the bitmap impl is deterministic.

## 9. References

- ext4 disk layout — kernel docs `Documentation/filesystems/ext4/`.
- squashfs format — kernel docs / `squashfs-tools`.
- EROFS on-disk format — kernel `fs/erofs/erofs_fs.h` / `erofs-utils`.
- exFAT specification — Microsoft (opened, 2019).
- ISO9660 / ECMA-119 and the Rock Ridge / SUSP extensions.
- OCI Image Format Specification.
- Reproducible builds — `SOURCE_DATE_EPOCH` convention.
