// Package partition writes GPT and MBR disk partition tables.
//
// A partition table is not a filesystem and does not implement
// image.Filesystem: it carves a whole-disk device.Device into aligned regions
// and hands each back as a device.Section, which the caller formats with any
// engine (ext, fat, …). This is the container layer that turns bare filesystem
// images into a bootable disk (e.g. GPT + ESP FAT32 + ext4 root). Use FormatGPT
// for a modern GUID partition table or FormatMBR for a classic table of up to
// four primaries.
//
// For GPT, GUIDs come from the injected image.UUIDSource, so a disk is
// reproducible by wiring; per-partition GUIDs are derived deterministically
// from the disk GUID to stay unique without extra entropy.
package partition
