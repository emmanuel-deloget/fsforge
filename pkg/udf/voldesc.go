package udf

// This file builds the ECMA-167 volume descriptors. Each returns the
// descriptor's bytes (smaller than a block; the device zero-pads the rest), with
// the tag stamped last over the populated content.

func (w *uwriter) pvd(loc uint32) []byte {
	b := make([]byte, 512)
	// volDescSeqNum (16) and primaryVolDescNum (20) = 0
	putDstring(b[24:56], w.label)
	le.PutUint16(b[56:], 1) // volSeqNum
	le.PutUint16(b[58:], 1) // maxVolSeqNum
	le.PutUint16(b[60:], 2) // interchangeLvl
	le.PutUint16(b[62:], 3) // maxInterchangeLvl
	le.PutUint32(b[64:], 1) // charSetList
	le.PutUint32(b[68:], 1) // maxCharSetList
	putDstring(b[72:200], w.volSetIdent())
	putCharSpecOSTA(b[200:264])
	putCharSpecOSTA(b[264:328])
	// volAbstract (328) and volCopyright (336) extents = 0
	putRegid(b[344:376], "*fsforge", impSuffix())
	putTimestamp(b[376:388], w.now)
	putRegid(b[388:420], "*fsforge", impSuffix())
	// impUse, predecessor, flags = 0
	setTag(b, tagPVD, 1, loc, 512-16)
	return b
}

func (w *uwriter) lvd(loc uint32) []byte {
	b := make([]byte, 446)
	putCharSpecOSTA(b[20:84])
	putDstring(b[84:212], w.label)
	le.PutUint32(b[212:], blockSize)
	putRegid(b[216:248], "*OSTA UDF Compliant", domainSuffix())
	putLongAD(b[248:264], blockSize, 0, 0) // logicalVolContentsUse -> FSD at partition lbn 0
	le.PutUint32(b[264:], 6)               // mapTableLength (one Type-1 map)
	le.PutUint32(b[268:], 1)               // numPartitionMaps
	putRegid(b[272:304], "*fsforge", impSuffix())
	putExtentAD(b[432:440], blockSize, lvidBlock) // integritySeqExt
	// Type-1 partition map (ECMA-167 3/10.7.2).
	b[440] = 1               // map type
	b[441] = 6               // map length
	le.PutUint16(b[442:], 1) // volSeqNum
	le.PutUint16(b[444:], 0) // partitionNum
	setTag(b, tagLVD, 1, loc, len(b)-16)
	return b
}

func (w *uwriter) pd(loc uint32) []byte {
	b := make([]byte, 512)
	le.PutUint16(b[20:], pdFlagsAlloc)
	le.PutUint16(b[22:], 0) // partitionNumber
	putRegid(b[24:56], "+NSR03", nil)
	// partitionContentsUse (56, partitionHeaderDesc) stays zero: a read-only
	// partition carries no unallocated-space set.
	le.PutUint32(b[184:], pdAccessReadOnly)
	le.PutUint32(b[188:], partBlock)
	le.PutUint32(b[192:], w.partitionLen)
	putRegid(b[196:228], "*fsforge", impSuffix())
	setTag(b, tagPD, 1, loc, 512-16)
	return b
}

const pdFlagsAlloc = 0x0001

func (w *uwriter) usd(loc uint32) []byte {
	b := make([]byte, 24)
	// volDescSeqNum (16) = 0, numAllocDescs (20) = 0
	setTag(b, tagUSD, 1, loc, len(b)-16)
	return b
}

func (w *uwriter) iuvd(loc uint32) []byte {
	b := make([]byte, 512)
	putRegid(b[20:52], "*UDF LV Info", udfSuffix())
	putCharSpecOSTA(b[52:116])
	putDstring(b[116:244], w.label)
	// LVInfo1/2/3 (244, 280, 316) left empty
	putRegid(b[352:384], "*fsforge", impSuffix())
	setTag(b, tagIUVD, 1, loc, 512-16)
	return b
}

func (w *uwriter) td(loc uint32) []byte {
	b := make([]byte, 512)
	setTag(b, tagTD, 1, loc, 512-16)
	return b
}

func (w *uwriter) avdp(loc uint32) []byte {
	b := make([]byte, 512)
	putExtentAD(b[16:24], mvdsLen*blockSize, mvdsBlock)
	putExtentAD(b[24:32], mvdsLen*blockSize, rvdsBlock)
	setTag(b, tagAVDP, 1, loc, 512-16)
	return b
}

func (w *uwriter) lvid(loc uint32) []byte {
	b := make([]byte, 134)
	putTimestamp(b[16:], w.now)
	le.PutUint32(b[28:], 1)              // integrityType = close
	le.PutUint64(b[40:], w.nextUnique)   // logicalVolHeaderDesc.uniqueID
	le.PutUint32(b[72:], 1)              // numOfPartitions
	le.PutUint32(b[76:], 46)             // lengthOfImpUse
	le.PutUint32(b[80:], 0)              // freeSpaceTable[0] (read-only: no free space)
	le.PutUint32(b[84:], w.partitionLen) // sizeTable[0]
	// Implementation Use (UDF 2.2.6.4).
	putRegid(b[88:120], "*fsforge", impSuffix())
	le.PutUint32(b[120:], w.numFiles)
	le.PutUint32(b[124:], w.numDirs)
	le.PutUint16(b[128:], udfRevision)
	le.PutUint16(b[130:], udfRevision)
	le.PutUint16(b[132:], udfRevision)
	setTag(b, tagLVID, 1, loc, len(b)-16)
	return b
}

func (w *uwriter) fsd(loc uint32) []byte {
	b := make([]byte, 512)
	putTimestamp(b[16:], w.now)
	le.PutUint16(b[28:], 3) // interchangeLvl
	le.PutUint16(b[30:], 3) // maxInterchangeLvl
	le.PutUint32(b[32:], 1) // charSetList
	le.PutUint32(b[36:], 1) // maxCharSetList
	// fileSetNum (40), fileSetDescNum (44) = 0
	putCharSpecOSTA(b[48:112])
	putDstring(b[112:240], w.label)
	putCharSpecOSTA(b[240:304])
	putDstring(b[304:336], w.label)
	// copyright (336) and abstract (368) file idents empty
	putLongAD(b[400:416], blockSize, 1, 0) // rootDirectoryICB -> root FE at lbn 1
	putRegid(b[416:448], "*OSTA UDF Compliant", domainSuffix())
	// nextExt (448) and streamDirectoryICB (464) = 0
	setTag(b, tagFSD, 1, loc, 512-16)
	return b
}
