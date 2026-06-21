// Package fsforge is the high-level entry point for building and converting
// filesystem images in pure Go — no root, no cgo, no external tools.
//
// It offers two levels of API. The Builder is the simple path: pick a
// filesystem type, point it at a directory, get an image.
//
//	err := fsforge.New("ext4").
//		Reproducible(fsforge.SourceDateEpoch()).
//		Size("256M").Label("root").
//		BuildFromDir("./rootfs", "root.img")
//
// Convert bridges formats through a shared logical tree:
//
//	err := fsforge.Convert(
//		fsforge.Location{Kind: "oci", Path: "./alpine-oci"},
//		fsforge.Location{Kind: "ext4", Path: "rootfs.img"},
//		fsforge.Options{Size: "256M"},
//	)
//
// For finer control, the same building blocks are exported individually:
// EngineFor selects an engine, PopulateFromDir / Graft fill an image tree, and
// ExtractToDir writes a tree back to a host directory. HostDeps and
// ReproducibleDeps choose the injected clock and UUID source; reproducibility
// is purely a matter of which dependencies are wired, never a flag inside an
// engine.
//
// The lower-level contracts live under pkg/: image (Filesystem/Image/Dir),
// tree (the logical model), device (block backends), alloc and compress. The
// per-format engines are in pkg/ext, pkg/squashfs, pkg/fat, pkg/exfat and
// pkg/iso.
package fsforge
