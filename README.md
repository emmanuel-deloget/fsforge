# fsforge

A **pure-Go library** (plus a thin CLI) for **building and offline-mutating
filesystem images** — no root, no cgo, no shelling out, and **reproducible by
construction**.

fsforge exists because Go has no dependable, privilege-free way to *produce*
valid filesystem images. The driving use case is fully controlled image
generation in unprivileged CI: OS/appliance images, container & embedded
rootfs, VM disks, and reproducible build artifacts — on any host OS.

> Status: early scaffolding. The architecture and the public contracts are in
> place; engines are being implemented per the roadmap.

## Principles

- **Write or nothing** — only filesystems we can *write* are supported (no
  read-only formats).
- **Offline only** — images are never mounted while fsforge touches them, which
  removes crash-consistency and journal-recovery complexity.
- **Dependency injection everywhere** — IO, clock, identifiers, allocation and
  compression are all injected, so engines are deterministic and testable.
- **Reproducible by wiring** — identical inputs produce byte-identical output;
  no special mode, just the deterministic implementations wired in.

See **[doc/architecture.md](doc/architecture.md)** for the full design and the
filesystem roadmap (ext2 → ext4 → offline mutation → squashfs → exFAT → FAT/ISO).

## Layout

```
cmd/fsforge/   CLI (thin shell over the library)
pkg/           public packages: device, tree, image, alloc, compress, ext, squashfs
internal/      private helpers (binio) and the privileged test harness (conformance)
doc/           architecture & design notes
```

## Build

```bash
go build ./...
go test ./...
```
