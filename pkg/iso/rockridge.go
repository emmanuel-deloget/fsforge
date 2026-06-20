package iso

import (
	"io/fs"
	"strings"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// --- ISO9660 identifiers (the Rock Ridge NM entry carries the real name) ---

// isoIdentifier returns the on-disk ISO9660 identifier bytes for an entry. The
// real (long, cased) name travels in the Rock Ridge NM entry; this is only a
// compliant short identifier. "." and ".." map to the special 0x00/0x01 bytes.
func isoIdentifier(n *image.Node, name string) string {
	if name == "." {
		return "\x00"
	}
	if name == ".." {
		return "\x01"
	}
	if n != nil && n.IsDir() {
		return capStr(sanitizeISO(name, false), 31)
	}
	base, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		base, ext = name[:i], name[i+1:]
	}
	id := capStr(sanitizeISO(base, false), 30)
	if ext != "" {
		id += "." + capStr(sanitizeISO(ext, false), 28)
	}
	return id + ";1"
}

func sanitizeISO(s string, allowDot bool) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == '.' && allowDot:
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func capStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// --- Rock Ridge system-use entries ---

const (
	pxLen = 36
	tfLen = 5 + 7
	spLen = 7
	erID  = "RRIP_1991A" // identifies the Rock Ridge extension to readers
	erLen = 8 + len(erID)
)

func (l *layouter) suaLen(n *image.Node, name string, isRootDot bool) int {
	sua := pxLen + tfLen
	if isRootDot {
		sua += spLen + erLen
	}
	if name != "." && name != ".." {
		sua += 5 + len(name) // NM
		if n.Mode&fs.ModeSymlink != 0 {
			sua += slLen(n.Link)
		}
		if n.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0 {
			sua += 20 // PN
		}
	}
	return sua
}

func (l *layouter) writeSUA(b []byte, n *image.Node, name string, isRootDot bool) {
	off := 0
	if isRootDot {
		copy(b[off:], []byte{'S', 'P', spLen, 1, 0xBE, 0xEF, 0})
		off += spLen
		// ER: announce Rock Ridge so readers interpret the PX/NM/SL/TF entries.
		er := b[off : off+erLen]
		er[0], er[1], er[2], er[3] = 'E', 'R', byte(erLen), 1
		er[4] = byte(len(erID)) // LEN_ID
		er[5] = 0               // LEN_DES
		er[6] = 0               // LEN_SRC
		er[7] = 1               // EXT_VER
		copy(er[8:], erID)
		off += erLen
	}
	// PX
	px := b[off : off+pxLen]
	px[0], px[1], px[2], px[3] = 'P', 'X', pxLen, 1
	putBoth32(px[4:], rrMode(n))
	putBoth32(px[12:], uint32(maxInt(n.Nlink, 1)))
	putBoth32(px[20:], n.UID)
	putBoth32(px[28:], n.GID)
	off += pxLen
	// TF (modify time only)
	tf := b[off : off+tfLen]
	tf[0], tf[1], tf[2], tf[3], tf[4] = 'T', 'F', tfLen, 1, 0x02
	putDirTime(tf[5:], nodeTime(n, l.deps))
	off += tfLen

	if name == "." || name == ".." {
		return
	}
	// NM
	nm := b[off : off+5+len(name)]
	nm[0], nm[1], nm[2], nm[3], nm[4] = 'N', 'M', byte(5+len(name)), 1, 0
	copy(nm[5:], name)
	off += 5 + len(name)

	if n.Mode&fs.ModeSymlink != 0 {
		off += writeSL(b[off:], n.Link)
	}
	if n.Mode&(fs.ModeDevice|fs.ModeCharDevice) != 0 {
		pn := b[off : off+20]
		pn[0], pn[1], pn[2], pn[3] = 'P', 'N', 20, 1
		putBoth32(pn[4:], uint32(n.Rdev>>32))
		putBoth32(pn[12:], uint32(n.Rdev))
		off += 20
	}
}

// slLen / writeSL handle the Rock Ridge symlink ("SL") entry component area.
func slComponents(target string) []string {
	return strings.Split(target, "/")
}

func slLen(target string) int {
	n := 5 // SL header: sig(2)+len(1)+ver(1)+flags(1)
	for _, c := range slComponents(target) {
		n += 2 + len(c) // component flags(1)+len(1)+content
	}
	return n
}

func writeSL(b []byte, target string) int {
	total := slLen(target)
	b[0], b[1], b[2], b[3], b[4] = 'S', 'L', byte(total), 1, 0
	off := 5
	for _, c := range slComponents(target) {
		var flags byte
		content := c
		if c == "" { // leading empty => absolute root component
			flags = 0x08 // ROOT
		} else if c == "." {
			flags = 0x02 // CURRENT
			content = ""
		} else if c == ".." {
			flags = 0x04 // PARENT
			content = ""
		}
		b[off] = flags
		b[off+1] = byte(len(content))
		copy(b[off+2:], content)
		off += 2 + len(content)
	}
	return total
}

// rrMode builds the POSIX st_mode for a Rock Ridge PX entry.
func rrMode(n *image.Node) uint32 {
	m := uint32(n.Mode.Perm())
	if n.Mode&fs.ModeSetuid != 0 {
		m |= 0o4000
	}
	if n.Mode&fs.ModeSetgid != 0 {
		m |= 0o2000
	}
	if n.Mode&fs.ModeSticky != 0 {
		m |= 0o1000
	}
	switch {
	case n.Mode&fs.ModeDir != 0:
		m |= 0o040000
	case n.Mode&fs.ModeSymlink != 0:
		m |= 0o120000
	case n.Mode&fs.ModeCharDevice != 0:
		m |= 0o020000
	case n.Mode&fs.ModeDevice != 0:
		m |= 0o060000
	case n.Mode&fs.ModeNamedPipe != 0:
		m |= 0o010000
	case n.Mode&fs.ModeSocket != 0:
		m |= 0o140000
	default:
		m |= 0o100000
	}
	return m
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- path table identifiers ---

func pathTableID(d *dirNode) string {
	if d.parent == d { // root
		return "\x00"
	}
	return isoIdentifier(d.node, d.name)
}

// --- timestamps ---

func nodeTime(n *image.Node, deps image.Deps) time.Time {
	if n != nil && !n.ModTime.IsZero() {
		return n.ModTime
	}
	return deps.Clock.Now()
}

func putDirTime(b []byte, t time.Time) {
	t = t.UTC()
	b[0] = byte(t.Year() - 1900)
	b[1] = byte(t.Month())
	b[2] = byte(t.Day())
	b[3] = byte(t.Hour())
	b[4] = byte(t.Minute())
	b[5] = byte(t.Second())
	b[6] = 0 // GMT offset (15-min units)
}

func volTime(t time.Time) []byte {
	t = t.UTC()
	s := t.Format("20060102150405") + "00"
	out := make([]byte, 17)
	copy(out, s)
	out[16] = 0
	return out
}

func zeroVolTime() []byte {
	out := make([]byte, 17)
	for i := 0; i < 16; i++ {
		out[i] = '0'
	}
	out[16] = 0
	return out
}
