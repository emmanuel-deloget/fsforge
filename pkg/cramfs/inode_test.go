package cramfs

import (
	"io/fs"
	"testing"
)

func TestInodeRoundTrip(t *testing.T) {
	cases := []cinode{
		{mode: sIFDIR | 0o755, uid: 1000, size: 4096, gid: 100, namelen: 1, offset: 19},
		{mode: sIFREG | 0o644, uid: 0xFFFF, size: 0xFFFFFF, gid: 0xFF, namelen: 0x3F, offset: 0x3FFFFFF},
		{mode: sIFCHR | 0o666, size: 0x0103},
	}
	for _, c := range cases {
		got := parseInode(c.marshal())
		if got != c {
			t.Errorf("inode round-trip:\n got %+v\nwant %+v", got, c)
		}
	}
}

func TestModeRoundTrip(t *testing.T) {
	modes := []fs.FileMode{
		0o644, fs.ModeDir | 0o755, fs.ModeSymlink | 0o777,
		fs.ModeCharDevice | fs.ModeDevice | 0o666, fs.ModeDevice | 0o660,
		fs.ModeNamedPipe | 0o644, fs.ModeSocket | 0o755,
		fs.ModeSetuid | 0o755, fs.ModeSetgid | 0o755, fs.ModeSticky | 0o777,
	}
	for _, m := range modes {
		if got := modeFromUnix(modeToUnix(m)); got != m {
			t.Errorf("mode %v round-tripped to %v", m, got)
		}
	}
}
