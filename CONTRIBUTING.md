# Contributing to fsforge

Thanks for looking. This is a small project with a narrow remit, and the notes
below are mostly about that remit — what fsforge is for, and what it declines to
become — because a patch that fits takes far less of everybody's time than one
that has to be talked out of scope afterwards.

## Before a large change

Open an issue first. Not for permission — for the chance that the thing you want
is already half-built somewhere, or that it conflicts with a rule below in a way
that is easier to say than to review.

Small fixes need no preamble. Send them.

## The rules that are not negotiable

These are in [doc/architecture.md](doc/architecture.md) with their reasoning.
The short version:

- **Pure Go.** No cgo, no shelling out, no third-party module. `go.mod` has no
  `require` block and there is no `go.sum`; that is a feature, and it is
  checked. External tools appear only in `internal/conformance`, under a build
  tag, in tests.
- **Write or nothing.** Every supported format is a write target. A read-only
  parser for a format fsforge cannot produce does not belong here.
- **Offline only.** No mounted or online operation. Journals are replayed on
  load and written fresh on finalize, never recovered transactionally.
- **Dependency injection.** Environment and policy arrive as interfaces —
  `device.Device`, `image.Clock`, `image.UUIDSource`, `alloc.Factory`,
  `compress.Compressor`. Format-mandated algorithms (crc32c, half_md4, struct
  encoding) stay unexported and golden-tested; they are not policy.
- **Reproducibility by wiring, not by flag.** No `reproducible` boolean inside an
  engine. Never call `time.Now()` or unseeded randomness there.
- **No buffering whole files.** Contents flow through `tree.Source` and are
  streamed at finalize. `TestStreamingKeepsMemoryBounded` fails if that stops
  being true.

## Building and testing

```bash
go build ./...
go test ./...                              # pure Go, unprivileged
go test -tags conformance ./...            # runs the real tools
bash scripts/coverage_gate.sh              # 80% per package
go test -run '^$' -bench . -benchmem .     # benchmarks
```

The conformance tests use a host binary when there is one and a container
otherwise, and skip when there is neither. They are where `e2fsck`,
`unsquashfs`, `fsck.erofs`, `xorriso` and friends get to disagree with us.

CI runs `go vet`, `gofmt`, the suite, the coverage gate, `govulncheck` and the
conformance tests, plus a job that builds an image through the GitHub Action.

## What a good test looks like here

This matters more than usual in this project, so it is worth being direct about.

**Coverage measures execution, not correctness.** Most of the format bugs fixed
in this repository were in code the tests already ran — a writer and a reader
that share a misreading of a format agree with each other perfectly, and every
assertion passes. The tests that find those are the differential ones, which
compare a full manifest of what went in against what came back, and the
conformance ones, which let somebody else's tool be the judge.

So, in rough order of how much they are worth:

1. **A conformance test.** Does `e2fsck` accept it? Does `unsquashfs` extract
   what we put in?
2. **A differential test.** Add the case to `internal/fsgen` if it is a shape
   the generator does not produce yet — a name at the length limit, a hard link,
   a device node.
3. **A unit test**, for the encoding itself.

**Check that your test fails without your fix.** Remove the fix, run the test,
watch it fail, put the fix back. Several tests in this repository were rewritten
after that check showed they were passing for an unrelated reason — a `..` entry
that failed on `EISDIR` rather than on the validation being tested. A test that
cannot fail is worse than no test, because it reads like a guarantee.

**Say why in the comment, not what.** The code says what it does. A comment
earns its place by recording what would otherwise be rediscovered: which
specification demanded this, what broke before, why the obvious version is
wrong.

## Commits

Sign off (DCO) and GPG-sign:

```bash
git commit -s -S
```

The subject line follows [Conventional
Commits](https://www.conventionalcommits.org/) — `feat(ext):`, `fix(security):`,
`test(cmd):`, `docs:` — and the body explains the *why*. If a commit changes
what fsforge writes to disk, say so plainly and say what it means for images
already built; that goes in `CHANGELOG.md` too.

Commits produced with AI assistance carry a co-author trailer:

```
Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
```

## Adding a format

Roughly, in the order that hurts least:

1. Read the on-disk layout properly. Kernel documentation beats a blog post.
2. Implement `image.Filesystem` — `Format` and `Open` returning the same
   editable `Image`.
3. Add it to `EngineFor` and to the differential table in
   `differential_test.go`, stating what the format can hold and what a round
   trip must preserve. The gap between those two is the format's honest
   capability list, and it belongs in the test rather than in a comment.
4. Add a conformance test against whatever real tool exists for it.
5. Update the table in `README.md` and the layout notes in
   `doc/architecture.md`.

## Licence

MIT. By contributing you agree your work is released under it, and the DCO
sign-off is how you say so.
