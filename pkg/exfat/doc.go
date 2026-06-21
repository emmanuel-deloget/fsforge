// Package exfat implements an exFAT create engine behind image.Filesystem.
//
// Like FAT, exFAT is a DOS-lineage filesystem with no owners, permissions,
// symlinks or hard links, so those tree attributes are dropped (symlinks are an
// error). It targets large volumes and removable media. fsforge writes every
// object contiguously with the NoFatChain flag, so the FAT holds only its two
// reserved entries and the allocation bitmap is the authority for used space.
//
// The layout is deterministic (sequential cluster allocation, timestamps from
// node ModTime) for reproducible images. The up-case table covers ASCII case
// folding, which matches typical filenames; name hashes use the same table.
package exfat
