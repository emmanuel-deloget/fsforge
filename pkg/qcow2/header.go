package qcow2

import "fmt"

// header is the subset of the QCOW2 v3 header fsforge sets and reads. All
// fields are big-endian on disk.
type header struct {
	version          uint32
	clusterBits      uint32
	size             uint64 // virtual disk size in bytes
	l1Size           uint32
	l1TableOffset    uint64
	refTableOffset   uint64
	refTableClusters uint32
	refcountOrder    uint32
}

func (h header) marshal() []byte {
	b := make([]byte, clusterSize) // a full cluster; the tail stays zero
	be.PutUint32(b[0:], magic)
	be.PutUint32(b[4:], h.version)
	// b[8:16]  backing_file_offset = 0
	// b[16:20] backing_file_size   = 0
	be.PutUint32(b[20:], h.clusterBits)
	be.PutUint64(b[24:], h.size)
	// b[32:36] crypt_method = 0
	be.PutUint32(b[36:], h.l1Size)
	be.PutUint64(b[40:], h.l1TableOffset)
	be.PutUint64(b[48:], h.refTableOffset)
	be.PutUint32(b[56:], h.refTableClusters)
	// b[60:64] nb_snapshots = 0
	// b[64:72] snapshots_offset = 0
	// b[72:80] incompatible_features = 0
	// b[80:88] compatible_features = 0
	// b[88:96] autoclear_features = 0
	be.PutUint32(b[96:], h.refcountOrder)
	be.PutUint32(b[100:], headerLen)
	// b[104] compression_type = 0 (zlib); b[105:112] padding
	// Header extensions are omitted: the zero word at headerLen ends them.
	return b
}

func parseHeader(b []byte) (header, error) {
	var h header
	if len(b) < headerLen {
		return h, fmt.Errorf("qcow2: short header")
	}
	if be.Uint32(b[0:]) != magic {
		return h, fmt.Errorf("qcow2: bad magic")
	}
	h.version = be.Uint32(b[4:])
	if h.version != 2 && h.version != version3 {
		return h, fmt.Errorf("qcow2: unsupported version %d", h.version)
	}
	h.clusterBits = be.Uint32(b[20:])
	if h.clusterBits < 9 || h.clusterBits > 21 {
		return h, fmt.Errorf("qcow2: implausible cluster_bits %d", h.clusterBits)
	}
	h.size = be.Uint64(b[24:])
	h.l1Size = be.Uint32(b[36:])
	h.l1TableOffset = be.Uint64(b[40:])
	h.refTableOffset = be.Uint64(b[48:])
	h.refTableClusters = be.Uint32(b[56:])
	if h.version == version3 {
		h.refcountOrder = be.Uint32(b[96:])
	} else {
		h.refcountOrder = 4
	}
	return h, nil
}
