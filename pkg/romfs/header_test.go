package romfs

import (
	"io/fs"
	"testing"
)

// TestChecksumGolden reproduces the checksum genromfs stored in the root header
// of a reference image: header words next=0x49, spec=0x20, size=0, then the
// name region for "." — the checksum that makes them sum to zero is 0xd1ffff97.
func TestChecksumGolden(t *testing.T) {
	buf := make([]byte, 32) // 16-byte header + 16-byte name region
	putHeader(buf, 0x40, typeDir|flagExec, 0x20, 0)
	copy(buf[16:], ".")
	fixChecksum(buf, 12, 32)
	if got := be.Uint32(buf[12:]); got != 0xd1ffff97 {
		t.Errorf("checksum = %#08x, want 0xd1ffff97", got)
	}
	if checksum(buf) != 0 {
		t.Errorf("words do not sum to zero after fixup")
	}
}

func TestTypeMode(t *testing.T) {
	cases := []struct {
		mode fs.FileMode
		typ  uint32
		exec bool
	}{
		{0o644, typeReg, false},
		{0o755, typeReg, true},
		{fs.ModeDir | 0o755, typeDir, true},
		{fs.ModeSymlink | 0o777, typeSymlink, false},
		{fs.ModeCharDevice | fs.ModeDevice | 0o666, typeChar, false},
		{fs.ModeDevice | 0o660, typeBlock, false},
		{fs.ModeNamedPipe | 0o644, typeFifo, false},
		{fs.ModeSocket | 0o755, typeSocket, false},
	}
	for _, c := range cases {
		typ, exec := typeOf(c.mode)
		if typ != c.typ || exec != c.exec {
			t.Errorf("typeOf(%v) = (%d,%v), want (%d,%v)", c.mode, typ, exec, c.typ, c.exec)
		}
		// The reconstructed mode keeps the type bits.
		if got := modeOf(typ, exec); got&fs.ModeType != c.mode&fs.ModeType {
			t.Errorf("modeOf(%d) type = %v, want %v", typ, got&fs.ModeType, c.mode&fs.ModeType)
		}
	}
}

func TestPaddedName(t *testing.T) {
	for name, want := range map[string]uint32{"": 16, ".": 16, "hosts": 16, "0123456789abcde": 16, "0123456789abcdef": 32} {
		if got := paddedName(name); got != want {
			t.Errorf("paddedName(%q) = %d, want %d", name, got, want)
		}
	}
}
