// Package conformance is the privileged, build-tagged test harness that
// validates fsforge output against the official tools for each filesystem.
//
// It is the heart of the project's correctness story and runs only in CI under
// the appropriate build tag (never as part of `go test ./...` on a developer
// machine without privileges). It drives, per engine:
//
//   - structural validators: e2fsck -fn, unsquashfs, fsck.exfat;
//   - loopback mount + read-back comparison of the logical tree;
//   - differential checks against the reference tool (mke2fs -d, mksquashfs)
//     compared structurally (dumpe2fs, debugfs stat, unsquashfs -ll), not
//     byte-for-byte;
//   - cross round-trips: fsforge-writes / tool-reads, and tool-writes /
//     fsforge-reads / fsforge-mutates / tool-validates.
//
// These external tools are invoked only by tests; the library itself shells out
// to nothing and is pure Go.
package conformance
