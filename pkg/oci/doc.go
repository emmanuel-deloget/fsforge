// Package oci reads and writes OCI container images, using the shared tree
// model as the hub between formats.
//
// An OCI image is not a block filesystem, so it does not implement
// image.Filesystem. It is a content-addressed layout (oci-layout, index.json,
// blobs/sha256/<digest>) whose root filesystem is a stack of tar layers applied
// with overlay/whiteout semantics. The package therefore exposes two halves:
//
//   - Build: a tree (built with the same image.Dir API as ext/squashfs) becomes
//     a tar layer plus config/manifest/index blobs in an OCI layout.
//   - Flatten: an existing layout's layers are applied in order into one tree,
//     which can then be serialised to ext4, squashfs, or back to OCI.
//
// This makes fsforge a converter: dir/ext/squashfs/oci → tree → dir/ext/
// squashfs/oci. Everything is pure Go, offline, rootless and reproducible by
// wiring (fixed clock + deterministic ordering). Registry transport is out of
// scope here; the package operates on local layouts (as produced by
// `podman save --format oci-dir`).
package oci
