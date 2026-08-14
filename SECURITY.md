# Security policy

## What fsforge treats as untrusted

fsforge parses filesystem images, OCI layers and registry responses. All three
are attacker-controlled in ordinary use — an image pulled from a registry, a
layer from a public base, a disk image someone handed you — and the library is
written on that basis. Specifically:

- **Every name in an image is untrusted.** Engines validate directory entries as
  they parse them (`image.ValidName`), because a name like `..` corrupts the
  directory it lands in and escapes the destination once `ExtractToDir` joins it
  onto a host path.
- **Every size and count in an image is untrusted.** A parser must not allocate
  what a header claims without bounding it.
- **Registry responses are untrusted.** Blobs are verified against the digest
  that named them, streaming and bounded by the declared size, before they are
  used for anything.

## Reporting a vulnerability

Use GitHub's [private vulnerability
reporting](https://github.com/emmanuel-deloget/fsforge/security/advisories/new)
for anything that fits the scope below. It reaches the maintainer privately,
keeps the discussion attached to the repository, and turns into an advisory
without anything being retyped.

Please include what you would want to receive: the input that triggers it (an
image, a layer, a specification), the version or commit, and what you expected
to happen instead. A reproducer is worth more than a description.

You will get an acknowledgement within a week. Since fsforge is maintained by
one person in their own time, a fix may take longer than that; you will be told
where it stands rather than left waiting.

Please do not open a public issue for something exploitable until there is a
fix, and do not run tests against systems you do not own.

## In scope

- Reading a crafted image, layer or specification causes a panic, an unbounded
  allocation, or a loop that does not terminate.
- Extraction (`ExtractToDir`, the OCI flatten, `fsforge convert -to dir:…`)
  writes outside its destination, follows a symlink out of it, or overwrites
  something it was not asked to.
- A registry, or anything between you and one, can make fsforge use content that
  does not match the digest it was fetched under.
- A build marked reproducible is not — the same inputs producing different bytes
  is a supply-chain problem, not merely a defect.
- Credentials leak: into an image, into a log, into a process listing.

## Out of scope

- **Producing a malicious image on purpose.** fsforge writes the image it is
  asked for; a setuid binary or a device node in the output is a decision the
  caller made, not a vulnerability.
- **Mounting an image fsforge wrote.** Kernel filesystem drivers have their own
  attack surface and their own security processes. If a crafted fsforge image
  crashes a driver, that is worth reporting — to the driver's maintainers, and
  we will help.
- **The conformance test harness.** It shells out to `e2fsck`, `mksquashfs` and
  friends, and runs containers. It is test-only, never built into the library or
  the CLI, and it trusts its own inputs by design.
- Anything requiring an attacker who already has the privileges to write the
  files fsforge is reading.

## Supported versions

fsforge is pre-1.0 and the latest release is the supported one. Fixes land on
`main` and go out in the next release; there are no maintenance branches yet.

| Version | Supported |
|---------|-----------|
| latest release | ✅ |
| anything older | ❌ |

## What is already in place

- No cgo, no external process, no network outside `pkg/ociremote` — a build that
  does not mention a registry does not open a socket.
- No third-party dependencies at all, so there is no transitive supply chain to
  audit.
- `govulncheck` and `go vet` run in CI, including weekly, so an advisory
  published without a commit still surfaces.
- Path handling, digest verification and name validation each have adversarial
  tests, written to fail when the check is removed rather than merely to pass
  when it is present.
