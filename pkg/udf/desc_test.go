package udf

import (
	"testing"
	"time"
)

// TestCRCITUT checks the CRC against the standard CRC-16/XMODEM check value, the
// same algorithm (init 0) UDF uses for descriptor CRCs.
func TestCRCITUT(t *testing.T) {
	if got := crcITUT(0, []byte("123456789")); got != 0x31C3 {
		t.Errorf("crcITUT(\"123456789\") = %#04x, want 0x31c3", got)
	}
	if got := crcITUT(0, nil); got != 0 {
		t.Errorf("crcITUT(empty) = %#04x, want 0", got)
	}
}

// TestSetTag checks the tag checksum is the byte sum excluding the checksum
// field, and that the CRC/length are recorded.
func TestSetTag(t *testing.T) {
	buf := make([]byte, 64)
	for i := 16; i < 64; i++ {
		buf[i] = byte(i)
	}
	setTag(buf, tagFE, 1, 7, 48)

	if le.Uint16(buf[0:]) != tagFE || le.Uint16(buf[2:]) != descVersion {
		t.Errorf("tag ident/version wrong")
	}
	if le.Uint16(buf[10:]) != 48 {
		t.Errorf("descCRCLength = %d, want 48", le.Uint16(buf[10:]))
	}
	if le.Uint32(buf[12:]) != 7 {
		t.Errorf("tagLocation = %d, want 7", le.Uint32(buf[12:]))
	}
	if le.Uint16(buf[8:]) != crcITUT(0, buf[16:64]) {
		t.Errorf("descCRC mismatch")
	}
	var sum byte
	for i := 0; i < 16; i++ {
		if i != 4 {
			sum += buf[i]
		}
	}
	if buf[4] != sum {
		t.Errorf("checksum = %#x, want %#x", buf[4], sum)
	}
}

func TestPutDstring(t *testing.T) {
	dst := make([]byte, 32)
	putDstring(dst, "TESTVOL")
	if dst[0] != 8 {
		t.Errorf("compression id = %d, want 8", dst[0])
	}
	if string(dst[1:8]) != "TESTVOL" {
		t.Errorf("chars = %q", dst[1:8])
	}
	if dst[31] != 8 { // 1 compression byte + 7 chars
		t.Errorf("length byte = %d, want 8", dst[31])
	}

	zero := make([]byte, 32)
	putDstring(zero, "")
	for _, b := range zero {
		if b != 0 {
			t.Fatalf("empty dstring not all zero")
		}
	}
}

func TestCS0Bytes(t *testing.T) {
	if got := cs0Bytes("etc"); len(got) != 4 || got[0] != 8 || string(got[1:]) != "etc" {
		t.Errorf("cs0Bytes(etc) = %v", got)
	}
	wide := cs0Bytes("éĀ")
	if wide[0] != 16 {
		t.Errorf("wide compression id = %d, want 16", wide[0])
	}
}

func TestPutTimestamp(t *testing.T) {
	dst := make([]byte, 12)
	putTimestamp(dst, time.Date(2026, 6, 21, 13, 45, 7, 0, time.UTC))
	if le.Uint16(dst[2:]) != 2026 || dst[4] != 6 || dst[5] != 21 || dst[6] != 13 {
		t.Errorf("timestamp fields wrong: %v", dst)
	}
}
