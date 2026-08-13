package mtree

import (
	"bufio"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"time"
)

// Parse reads a specification.
//
// Both dialects are accepted. The classic one is hierarchical: a bare name is
// relative to the directory the previous lines established, and ".." goes back
// up. The 2.0 one, which libarchive and bsdtar emit, puts a full path on every
// line. They are told apart per line rather than up front, because real files
// mix them — a hierarchical spec still writes "." for its root.
//
// Unknown keywords are ignored, as the format requires: a spec written for
// another tool carries checksums and flags that mean nothing here, and refusing
// them would make interoperability the exception rather than the point.
func Parse(r io.Reader) (*Spec, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	spec := &Spec{}
	defaults := map[string]string{}
	var dir []string // the current directory, for the hierarchical dialect
	line := 0

	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields, err := splitFields(text, line)
		if err != nil {
			return nil, err
		}

		switch fields[0] {
		case "/set":
			for _, f := range fields[1:] {
				k, v, ok := strings.Cut(f, "=")
				if !ok {
					return nil, errAt(line, "/set needs keyword=value, got %q", f)
				}
				defaults[k] = v
			}
			continue
		case "/unset":
			for _, f := range fields[1:] {
				delete(defaults, f)
			}
			continue
		case "..":
			if len(dir) > 0 {
				dir = dir[:len(dir)-1]
			}
			continue
		}

		name, err := unvis(fields[0], line)
		if err != nil {
			return nil, err
		}
		kw, err := keywords(defaults, fields[1:], line)
		if err != nil {
			return nil, err
		}

		e, err := buildEntry(name, kw, dir, line)
		if err != nil {
			return nil, err
		}
		spec.Entries = append(spec.Entries, e)

		// A relative directory entry opens a level in the hierarchical dialect.
		if e.Type != nil && *e.Type == TypeDir && !strings.Contains(name, "/") && name != "." {
			dir = append(dir, name)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return spec, nil
}

// keywords merges a line's keywords over the current /set defaults.
func keywords(defaults map[string]string, fields []string, line int) (map[string]string, error) {
	out := make(map[string]string, len(defaults)+len(fields))
	for k, v := range defaults {
		out[k] = v
	}
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			// A bare keyword is legal in mtree (it means "present"); none of the
			// ones acted on here take that form, so record it and move on.
			out[f] = ""
			continue
		}
		out[k] = v
	}
	return out, nil
}

func buildEntry(name string, kw map[string]string, dir []string, line int) (Entry, error) {
	e := Entry{Line: line}

	// Resolve the path. A name holding a separator is already a full path; a
	// bare one hangs off whatever directory the hierarchical dialect is in.
	clean := strings.TrimPrefix(name, "./")
	if clean == "." {
		clean = ""
	} else if !strings.Contains(name, "/") {
		clean = path.Join(append(append([]string{}, dir...), clean)...)
	}
	clean = strings.Trim(path.Clean("/"+clean), "/")
	e.Path = clean

	for k, v := range kw {
		switch k {
		case "type":
			t, ok := typeNames[v]
			if !ok {
				return e, errAt(line, "unknown type %q", v)
			}
			e.Type = &t
		case "uid":
			n, err := parseUint(v, 10, line, "uid")
			if err != nil {
				return e, err
			}
			u := uint32(n)
			e.UID = &u
		case "gid":
			n, err := parseUint(v, 10, line, "gid")
			if err != nil {
				return e, err
			}
			g := uint32(n)
			e.GID = &g
		case "mode":
			n, err := parseUint(v, 8, line, "mode")
			if err != nil {
				return e, err
			}
			m := permFromUnix(uint32(n))
			e.Mode = &m
		case "link":
			s, err := unvis(v, line)
			if err != nil {
				return e, err
			}
			e.Link = &s
		case "size":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return e, errAt(line, "bad size %q", v)
			}
			e.Size = &n
		case "time":
			t, err := parseTime(v, line)
			if err != nil {
				return e, err
			}
			e.Time = &t
		case "device":
			major, minor, err := parseDevice(v, line)
			if err != nil {
				return e, err
			}
			e.Major, e.Minor = &major, &minor
		case "contents", "content":
			s, err := unvis(v, line)
			if err != nil {
				return e, err
			}
			e.Contents = &s
		default:
			// xattr.<name>=<base64-free value>: an extension libarchive writes and
			// the one unknown keyword worth acting on, since it is the whole point
			// of carrying attributes a checkout cannot hold.
			if rest, ok := strings.CutPrefix(k, "xattr."); ok {
				val, err := unvis(v, line)
				if err != nil {
					return e, err
				}
				if e.Xattrs == nil {
					e.Xattrs = map[string][]byte{}
				}
				e.Xattrs[rest] = []byte(val)
			}
		}
	}
	return e, nil
}

func parseUint(s string, base, line int, what string) (uint64, error) {
	n, err := strconv.ParseUint(s, base, 32)
	if err != nil {
		return 0, errAt(line, "bad %s %q", what, s)
	}
	return n, nil
}

// parseTime reads mtree's "seconds.nanoseconds".
func parseTime(s string, line int) (time.Time, error) {
	secStr, nsecStr, _ := strings.Cut(s, ".")
	sec, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		return time.Time{}, errAt(line, "bad time %q", s)
	}
	var nsec int64
	if nsecStr != "" {
		if nsec, err = strconv.ParseInt(nsecStr, 10, 64); err != nil {
			return time.Time{}, errAt(line, "bad time %q", s)
		}
	}
	return time.Unix(sec, nsec).UTC(), nil
}

// parseDevice reads "format,major,minor", the format being a naming scheme for
// how the two numbers combine. Only the numbers are used here; the combining is
// each engine's business.
func parseDevice(s string, line int) (uint32, uint32, error) {
	parts := strings.Split(s, ",")
	if len(parts) < 3 {
		return 0, 0, errAt(line, "device needs format,major,minor, got %q", s)
	}
	major, err := strconv.ParseUint(parts[len(parts)-2], 0, 32)
	if err != nil {
		return 0, 0, errAt(line, "bad device major in %q", s)
	}
	minor, err := strconv.ParseUint(parts[len(parts)-1], 0, 32)
	if err != nil {
		return 0, 0, errAt(line, "bad device minor in %q", s)
	}
	return uint32(major), uint32(minor), nil
}

// permFromUnix turns an octal mode into Go permission bits, keeping the three
// bits Go stores outside the low nine.
func permFromUnix(m uint32) fs.FileMode {
	p := fs.FileMode(m & 0o777)
	if m&0o4000 != 0 {
		p |= fs.ModeSetuid
	}
	if m&0o2000 != 0 {
		p |= fs.ModeSetgid
	}
	if m&0o1000 != 0 {
		p |= fs.ModeSticky
	}
	return p
}

// unixFromPerm is the inverse, for writing.
func unixFromPerm(m fs.FileMode) uint32 {
	u := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		u |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		u |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		u |= 0o1000
	}
	return u
}

// splitFields splits a line on unescaped whitespace. A backslash escapes the
// next character, which is how a name with a space survives.
func splitFields(s string, line int) ([]string, error) {
	var out []string
	var cur strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteByte('\\')
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == ' ' || r == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if esc {
		return nil, errAt(line, "line ends with a dangling backslash")
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return nil, errAt(line, "empty line reached the parser")
	}
	return out, nil
}

// unvis decodes mtree's escaping: a backslash followed by three octal digits,
// or by one of the C escapes. Anything else after a backslash is that character
// literally, which is how a space or a backslash itself is written.
func unvis(s string, line int) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			out.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", errAt(line, "dangling backslash in %q", s)
		}
		c := s[i+1]
		switch {
		case c >= '0' && c <= '7':
			if i+3 >= len(s) {
				return "", errAt(line, "truncated octal escape in %q", s)
			}
			n, err := strconv.ParseUint(s[i+1:i+4], 8, 8)
			if err != nil {
				return "", errAt(line, "bad octal escape in %q", s)
			}
			out.WriteByte(byte(n))
			i += 3
		default:
			if r, ok := cEscapes[c]; ok {
				out.WriteByte(r)
			} else {
				out.WriteByte(c)
			}
			i++
		}
	}
	return out.String(), nil
}

var cEscapes = map[byte]byte{
	'n': '\n', 'r': '\r', 't': '\t', 'b': '\b', 'f': '\f', 'v': '\v', 'a': 0x07,
}
