package fsgen

import (
	"fmt"
	"strings"
)

// uniqueName returns a name no sibling has used yet, drawn from the shapes that
// actually break writers rather than from a uniform alphabet.
//
// The uniqueness set is global rather than per-directory, which is stricter
// than filesystems require but keeps a failure's path unambiguous in a report.
func (g *gen) uniqueName(depth int) (string, error) {
	for attempt := 0; attempt < 64; attempt++ {
		n := g.name(depth, attempt)
		if len(n) > g.o.Caps.MaxName {
			continue
		}
		if g.names[n] {
			continue
		}
		g.names[n] = true
		return n, nil
	}
	return "", fmt.Errorf("fsgen: no unique name after 64 attempts at depth %d", depth)
}

func (g *gen) name(depth, salt int) string {
	seq := len(g.names) + salt
	switch g.rnd.Intn(10) {
	case 0:
		// At the length limit. 255 bytes is where ext4's dirent and squashfs's
		// name field stop, and off-by-ones there are silent: the name comes back
		// one byte short and nothing else looks wrong.
		return longName(g.o.Caps.MaxName, byte('a'+seq%26))
	case 1:
		// One byte under the limit, to catch a writer that reserves the wrong
		// slot size for the terminator.
		return longName(g.o.Caps.MaxName-1, byte('a'+seq%26))
	case 2:
		if g.o.Caps.NonUTF8 {
			// Legal on Linux — anything but '/' and NUL — and illegal in UDF and
			// ISO 9660, whose names are encoded, not copied. A writer that runs
			// the bytes through a string conversion mangles these silently.
			return fmt.Sprintf("nonutf8-%d-\xff\xfe\x80", seq)
		}
		return fmt.Sprintf("plain-%d", seq)
	case 3:
		// Characters that are ordinary on Linux and special to shells, archive
		// formats and path parsers.
		return fmt.Sprintf("odd %d ~!@#$%%^&()[]{}'\"`;,+=-", seq)
	case 4:
		// Leading dots: not "." or ".." — those are rejected by construction —
		// but names a lazy prefix check mistakes for them.
		return fmt.Sprintf("..%d", seq)
	case 5:
		return fmt.Sprintf("UPPER%d", seq) // case sensitivity
	case 6:
		return fmt.Sprintf("with.several.dots.%d.tar.gz", seq)
	case 7:
		return fmt.Sprintf("uni-%d-héllo-日本語-🙂", seq) // multi-byte, valid UTF-8
	default:
		return fmt.Sprintf("file-%d", seq)
	}
}

func longName(n int, c byte) string {
	if n < 1 {
		n = 1
	}
	return strings.Repeat(string(c), n)
}
