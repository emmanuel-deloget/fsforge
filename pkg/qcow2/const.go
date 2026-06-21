// Package qcow2 implements a pure-Go QCOW2 (version 3) disk-image container as a
// device.Device. It is not a filesystem: it sits at the device layer, below
// partitions and engines, and encodes the virtual block device that fsforge
// would otherwise write to a raw file — sparsely, allocating a host cluster only
// when a region is actually written.
//
// A Writer is a writable device whose data clusters stream straight to the host
// file as engines write them, while the mapping metadata (L1/L2 tables and
// refcounts) is held in memory — bounded by the allocated size, never the whole
// image — and flushed by Finalize. A Reader presents an existing QCOW2 as a
// read-only device, so any engine can open a filesystem stored inside one and
// `fsforge disk`/`mkfs` can emit one. Compressed and encrypted images are out of
// scope; clusters are stored uncompressed.
package qcow2

import "encoding/binary"

const (
	magic    = 0x514649fb // "QFI\xfb"
	version3 = 3

	clusterBits = 16
	clusterSize = 1 << clusterBits // 65536
	headerLen   = 112              // QCOW2 v3 header incl. compression_type byte

	l2Entries      = clusterSize / 8 // 8-byte L2/L1 entries per cluster
	refcountOrder  = 4               // refcount_bits = 1<<4 = 16
	refcountsPerCl = clusterSize / 2 // 16-bit refcounts per refcount block

	// Entry flags and the cluster-offset mask shared by L1 and L2 entries.
	flagCopied     = uint64(1) << 63 // refcount == 1, safe to write in place
	flagCompressed = uint64(1) << 62
	flagZero       = uint64(1) << 0 // standard cluster reads as zeros
	offsetMask     = uint64(0x00fffffffffffe00)
)

var be = binary.BigEndian

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }
