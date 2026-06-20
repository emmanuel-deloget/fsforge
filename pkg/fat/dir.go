package fat

import (
	"strings"
	"time"
	"unicode/utf16"
)

// shortNamer generates unique 8.3 short names within one directory.
type shortNamer struct {
	used map[string]bool
}

func newShortNamer() *shortNamer { return &shortNamer{used: map[string]bool{}} }

// fits83 reports whether name is already a valid uppercase 8.3 name needing no
// LFN. Conservative: ASCII, allowed chars, <=8 base and <=3 ext.
func fits83(name string) (string, bool) {
	if name == "." || name == ".." || name == "" {
		return "", false
	}
	upper := strings.ToUpper(name)
	if upper != name {
		return "", false // had lowercase: keep the real name via LFN
	}
	base, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		base, ext = name[:i], name[i+1:]
	}
	if len(base) == 0 || len(base) > 8 || len(ext) > 3 || strings.Contains(ext, ".") {
		return "", false
	}
	for _, r := range name {
		if r == '.' {
			continue
		}
		if !validShortRune(r) {
			return "", false
		}
	}
	return pack83(base, ext), true
}

func validShortRune(r rune) bool {
	if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	return strings.ContainsRune("$%'-_@~`!(){}^#&", r)
}

// pack83 formats base/ext into the 11-byte on-disk short name (space padded).
func pack83(base, ext string) string {
	b := []byte("           ") // 11 spaces
	copy(b[0:8], base)
	copy(b[8:11], ext)
	return string(b)
}

// generate returns the 11-byte short name for a long name, mangling to
// BASE~N.EXT and ensuring uniqueness in the directory.
func (s *shortNamer) generate(name string) string {
	if sn, ok := fits83(name); ok && !s.used[sn] {
		s.used[sn] = true
		return sn
	}
	base, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		base, ext = name[:i], name[i+1:]
	}
	base = sanitize83(base, 8)
	ext = sanitize83(ext, 3)
	for n := 1; ; n++ {
		suffix := "~" + itoa(n)
		keep := 8 - len(suffix)
		if keep > len(base) {
			keep = len(base)
		}
		cand := pack83(base[:keep]+suffix, ext)
		if !s.used[cand] {
			s.used[cand] = true
			return cand
		}
	}
}

func sanitize83(s string, max int) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r == '.' || r == ' ' {
			continue
		}
		if !validShortRune(r) {
			r = '_'
		}
		b.WriteRune(r)
		if b.Len() >= max {
			break
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// shortChecksum is the LFN checksum over the 11-byte short name.
func shortChecksum(short string) byte {
	var sum byte
	for i := 0; i < 11; i++ {
		sum = byte((sum&1)<<7) + (sum >> 1) + short[i]
	}
	return sum
}

// lfnEntries builds the VFAT long-name entries (in on-disk order: last logical
// piece first) for name, referencing the given short-name checksum.
func lfnEntries(name string, checksum byte) [][dirEntrySize]byte {
	runes := utf16.Encode([]rune(name))
	// Pad to a multiple of 13 with a NUL terminator then 0xFFFF.
	const perEntry = 13
	total := (len(runes)/perEntry + 1) * perEntry
	padded := make([]uint16, total)
	copy(padded, runes)
	for i := len(runes); i < total; i++ {
		if i == len(runes) {
			padded[i] = 0x0000
		} else {
			padded[i] = 0xFFFF
		}
	}
	count := total / perEntry

	var out [][dirEntrySize]byte
	for seq := count; seq >= 1; seq-- {
		var e [dirEntrySize]byte
		ord := byte(seq)
		if seq == count {
			ord |= 0x40 // last logical entry marker
		}
		e[0] = ord
		e[11] = attrLongName
		e[13] = checksum
		chunk := padded[(seq-1)*perEntry : seq*perEntry]
		putUTF16(e[1:11], chunk[0:5])
		putUTF16(e[14:26], chunk[5:11])
		putUTF16(e[28:32], chunk[11:13])
		out = append(out, e)
	}
	return out
}

func putUTF16(dst []byte, src []uint16) {
	for i, v := range src {
		dst[i*2] = byte(v)
		dst[i*2+1] = byte(v >> 8)
	}
}

// fatTime encodes a time into FAT (date word, time word, tenths).
func fatTime(t time.Time) (date, tm uint16, tenth byte) {
	t = t.UTC()
	y := t.Year()
	if y < 1980 {
		y = 1980
	}
	date = uint16((y-1980)<<9) | uint16(t.Month())<<5 | uint16(t.Day())
	tm = uint16(t.Hour())<<11 | uint16(t.Minute())<<5 | uint16(t.Second()/2)
	tenth = byte((t.Second() % 2) * 100)
	return
}
