package exfat

// writeBoot builds and writes the main and backup boot regions (12 sectors
// each), including the boot checksum sector.
func (l *layouter) writeBoot(rootFirst uint32) error {
	g := l.geo
	region := make([]byte, 12*bytesPerSector)

	b := region[:bytesPerSector] // sector 0: main boot sector
	b[0], b[1], b[2] = 0xEB, 0x76, 0x90
	copy(b[3:11], "EXFAT   ")
	le.PutUint64(b[64:], 0) // partition offset
	le.PutUint64(b[72:], g.totalSectors)
	le.PutUint32(b[80:], uint32(g.fatOffset))
	le.PutUint32(b[84:], uint32(g.fatLength))
	le.PutUint32(b[88:], uint32(g.clusterHeapStart))
	le.PutUint32(b[92:], uint32(g.clusterCount))
	le.PutUint32(b[96:], rootFirst)
	u := l.deps.UUID.UUID()
	le.PutUint32(b[100:], le.Uint32(u[0:4])) // volume serial
	le.PutUint16(b[104:], 0x0100)            // FS revision 1.00
	le.PutUint16(b[106:], 0)                 // volume flags
	b[108] = bytesPerSectorShift
	b[109] = byte(g.spcShift)
	b[110] = 1 // number of FATs
	b[111] = 0x80
	b[112] = byte(l.usedClusters * 100 / g.clusterCount) // percent in use
	b[510], b[511] = 0x55, 0xAA

	// Extended boot sectors 1-8: signature 0xAA550000 at the end.
	for s := 1; s <= 8; s++ {
		es := region[s*bytesPerSector : (s+1)*bytesPerSector]
		es[508], es[509], es[510], es[511] = 0x00, 0x00, 0x55, 0xAA
	}
	// Sectors 9 (OEM) and 10 (reserved) stay zero.

	// Sector 11: boot checksum, the u32 repeated to fill the sector. The
	// checksum spans sectors 0-10, excluding VolumeFlags (106,107) and
	// PercentInUse (112).
	sum := bootChecksum(region[:11*bytesPerSector])
	csum := region[11*bytesPerSector : 12*bytesPerSector]
	for i := 0; i < bytesPerSector; i += 4 {
		le.PutUint32(csum[i:], sum)
	}

	if _, err := l.dev.WriteAt(region, 0); err != nil {
		return err
	}
	_, err := l.dev.WriteAt(region, 12*bytesPerSector) // backup region
	return err
}

// bootChecksum computes the exFAT boot region checksum, skipping the
// VolumeFlags and PercentInUse bytes in the main boot sector.
func bootChecksum(data []byte) uint32 {
	var sum uint32
	for i, b := range data {
		if i == 106 || i == 107 || i == 112 {
			continue
		}
		sum = (sum >> 1) | (sum << 31)
		sum += uint32(b)
	}
	return sum
}
