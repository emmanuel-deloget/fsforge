// Package fat implements a FAT32 create engine behind image.Filesystem.
//
// FAT is a DOS filesystem: it has no owners, permissions, symlinks or hard
// links, so those tree attributes are dropped when writing (symlinks are an
// error). Its primary use in fsforge is EFI System Partitions and boot/data
// volumes. Long names are stored with VFAT LFN entries plus a generated 8.3
// short name; the layout is deterministic for reproducible images.
//
// Only FAT32 is implemented (the variant that matters for ESPs); FAT12/16 can
// be added later behind the same engine.
package fat
