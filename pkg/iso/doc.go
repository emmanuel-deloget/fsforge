// Package iso implements an ISO9660 create engine with Rock Ridge extensions,
// behind image.Filesystem.
//
// ISO9660 is a write-once, read-only format, so like squashfs "mutation" means
// rebuilding. Rock Ridge (RRIP/SUSP) is emitted so POSIX names, permissions,
// symlinks and device nodes survive — the same tree model as the other engines.
// Files are stored as single contiguous extents (the format's natural shape).
//
// Limitations: directory depth is bounded at 8 (no deep-relocation), and a
// single directory record's system-use area must fit in 255 bytes (no CE
// continuation), which is ample for typical names. Joliet is not emitted; Rock
// Ridge covers the Linux use case.
package iso
