package udf

import "time"

// crcTable is the CRC-ITU-T (CRC-CCITT, polynomial 0x1021) table UDF uses for
// descriptor CRCs.
var crcTable = func() [256]uint16 {
	var t [256]uint16
	for i := 0; i < 256; i++ {
		c := uint16(i) << 8
		for k := 0; k < 8; k++ {
			if c&0x8000 != 0 {
				c = (c << 1) ^ 0x1021
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return t
}()

// crcITUT computes the CRC-ITU-T of data starting from crc (UDF passes 0).
func crcITUT(crc uint16, data []byte) uint16 {
	for _, b := range data {
		crc = (crc << 8) ^ crcTable[byte(crc>>8)^b]
	}
	return crc
}

// setTag fills the 16-byte descriptor tag at buf[0:16] for a descriptor whose
// content (after the tag) spans crcLen bytes, then computes the CRC over that
// content and the tag checksum (ECMA-167 3/7.2).
func setTag(buf []byte, ident uint16, serial uint16, location uint32, crcLen int) {
	le.PutUint16(buf[0:], ident)
	le.PutUint16(buf[2:], descVersion)
	buf[4] = 0 // checksum, filled below
	buf[5] = 0
	le.PutUint16(buf[6:], serial)
	le.PutUint16(buf[8:], crcITUT(0, buf[16:16+crcLen]))
	le.PutUint16(buf[10:], uint16(crcLen))
	le.PutUint32(buf[12:], location)
	var sum byte
	for i := 0; i < 16; i++ {
		if i != 4 {
			sum += buf[i]
		}
	}
	buf[4] = sum
}

// putRegid writes a 32-byte entity identifier (ECMA-167 1/7.4): a flags byte, a
// 23-byte identifier and an 8-byte suffix.
func putRegid(dst []byte, ident string, suffix []byte) {
	for i := range dst[:32] {
		dst[i] = 0
	}
	dst[0] = 0 // flags
	copy(dst[1:24], ident)
	copy(dst[24:32], suffix)
}

// domainSuffix is the identifier suffix for the "*OSTA UDF Compliant" domain.
func domainSuffix() []byte {
	s := make([]byte, 8)
	le.PutUint16(s[0:], udfRevision)
	// s[2] domainFlags = 0 (no write protection), rest reserved.
	return s
}

// udfSuffix is the identifier suffix for UDF entity identifiers: the revision
// plus the OS class/identifier (UNIX/Linux).
func udfSuffix() []byte {
	s := make([]byte, 8)
	le.PutUint16(s[0:], udfRevision)
	s[2] = 4 // OS class UNIX
	s[3] = 5 // OS identifier Linux
	return s
}

// impSuffix is the identifier suffix for implementation entity identifiers.
func impSuffix() []byte {
	s := make([]byte, 8)
	s[0] = 4 // OS class UNIX
	s[1] = 5 // OS identifier Linux
	return s
}

// putCharSpecOSTA writes the 64-byte OSTA CS0 character set specification.
func putCharSpecOSTA(dst []byte) {
	for i := range dst[:64] {
		dst[i] = 0
	}
	dst[0] = 0 // CS0
	copy(dst[1:], "OSTA Compressed Unicode")
}

// putDstring writes s into a fixed dstring field of len(dst) bytes (ECMA-167
// 1/7.2.12): the OSTA CS0 bytes at the front and the used length in the last
// byte. An empty string leaves the field zero.
func putDstring(dst []byte, s string) {
	for i := range dst {
		dst[i] = 0
	}
	if s == "" {
		return
	}
	cs0 := cs0Bytes(s)
	n := copy(dst[:len(dst)-1], cs0)
	dst[len(dst)-1] = byte(n)
}

// cs0Bytes encodes s as an OSTA CS0 string: a compression-id byte (8 when every
// rune fits in a byte, else 16) followed by the characters.
func cs0Bytes(s string) []byte {
	runes := []rune(s)
	wide := false
	for _, r := range runes {
		if r > 0xFF {
			wide = true
			break
		}
	}
	if !wide {
		out := make([]byte, 1+len(runes))
		out[0] = 8
		for i, r := range runes {
			out[1+i] = byte(r)
		}
		return out
	}
	out := make([]byte, 1+2*len(runes))
	out[0] = 16
	for i, r := range runes {
		out[1+2*i] = byte(r >> 8)
		out[2+2*i] = byte(r)
	}
	return out
}

// putTimestamp writes a 12-byte UDF timestamp (ECMA-167 1/7.3) for t in UTC.
func putTimestamp(dst []byte, t time.Time) {
	t = t.UTC()
	le.PutUint16(dst[0:], 0x1000) // type = local time, timezone 0 (UTC)
	le.PutUint16(dst[2:], uint16(t.Year()))
	dst[4] = byte(t.Month())
	dst[5] = byte(t.Day())
	dst[6] = byte(t.Hour())
	dst[7] = byte(t.Minute())
	dst[8] = byte(t.Second())
	cs := t.Nanosecond() / 10_000_000
	dst[9] = byte(cs)
	dst[10] = 0
	dst[11] = 0
}

// putShortAD writes an 8-byte short allocation descriptor (ECMA-167 4/14.14.1).
func putShortAD(dst []byte, length, position uint32) {
	le.PutUint32(dst[0:], length)
	le.PutUint32(dst[4:], position)
}

// putLongAD writes a 16-byte long allocation descriptor (ECMA-167 4/14.14.2)
// pointing at logical block lbn of partition partRef.
func putLongAD(dst []byte, length, lbn uint32, partRef uint16) {
	le.PutUint32(dst[0:], length)
	le.PutUint32(dst[4:], lbn)
	le.PutUint16(dst[8:], partRef)
	for i := 10; i < 16; i++ {
		dst[i] = 0
	}
}

// putExtentAD writes an 8-byte extent descriptor (ECMA-167 3/7.1).
func putExtentAD(dst []byte, length, location uint32) {
	le.PutUint32(dst[0:], length)
	le.PutUint32(dst[4:], location)
}
