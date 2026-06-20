# CLAUDE.md — fsforge

fsforge is a **pure-Go library (plus a thin CLI) for building and
offline-mutating filesystem images**, without root, cgo, or shelling out.

## Read this first

**[doc/architecture.md](doc/architecture.md) is the source of truth.** Read it
before making non-trivial changes. The invariants below are a summary; the
document explains the *why*.

## Non-negotiable invariants

- **Pure Go.** No cgo, no external process invocation in the library. External
  tools (`mkfs`, `fsck`, …) appear *only* in tests under `internal/conformance`.
- **Write or nothing.** Do not add read-only filesystem support. Every supported
  format must be a write target.
- **Offline only.** No mounted/online operation. Journals are never recovered
  transactionally: replay on load, write a fresh empty journal on finalize.
- **Dependency injection.** Environment and policy are received as interfaces,
  never concrete types: `device.Device`, `image.Clock`, `image.UUIDSource`,
  `alloc.Factory`, `compress.Compressor`, `image.Logger`. Do **not** inject
  format-mandated algorithms (crc32c, half_md4, struct encoding) — keep them
  unexported and golden-tested.
- **Reproducibility by wiring.** No `reproducible` flag inside engines. A
  reproducible build is `FixedClock` + `FixedUUID` + the deterministic bitmap
  allocator; a host build swaps in `SystemClock` + `RandomUUID`. Never call
  `time.Now()` or unseeded randomness inside an engine.
- **No buffering whole files.** File contents flow through `tree.Source`
  (`io.ReaderAt`), streamed at finalize. Only metadata lives in memory.

## Layout (golang-standards/project-layout)

- `cmd/fsforge/` — CLI, thin shell only.
- `pkg/{device,tree,image,alloc,compress}` — public contracts + shared impls.
- `pkg/{ext,squashfs,…}` — engines behind `image.Filesystem`.
- `internal/{binio,conformance}` — private helpers + privileged test harness.
- `doc/` — architecture & design notes.

## Build & test

```bash
go build ./...
go test ./...                          # pure-Go, unprivileged
go test -tags conformance ./pkg/ext/   # runs e2fsck (host binary or container)
```

The conformance tests validate ext images with real e2fsprogs: they use a host
`e2fsck` if present, otherwise a container runtime (podman/docker) pulling
e2fsprogs on demand. They skip when neither is available. squashfs is validated
by `unsquashfs` in its normal test.

## Commit conventions

- Sign off (DCO) **and** GPG-sign every commit:

  ```bash
  git commit -s -S
  ```

- Include a co-author trailer in the message:

  ```
  Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
  ```

- Don't commit or push unless asked. If on the default branch, branch first.
