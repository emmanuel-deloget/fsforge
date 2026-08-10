package iso

import "testing"

// TestSLRoundTrip pins the Rock Ridge symlink encoding. The declared entry
// length and the bytes written used to be computed separately, and disagreed
// by two bytes for every "." or ".." component: the reader took the trailing
// zeroes for one more, empty, component, so "../elsewhere" read back as
// "../elsewhere/". Every relative symlink in every image was affected.
func TestSLRoundTrip(t *testing.T) {
	targets := []string{
		"sibling",
		"../elsewhere",
		"../../two/levels/up",
		"./here",
		"/absolute/target",
		"a/b/c",
		"..",
		".",
	}
	for _, target := range targets {
		b := make([]byte, 512)
		n := writeSL(b, target)
		if n != slLen(target) {
			t.Errorf("%q: writeSL returned %d, slLen says %d", target, n, slLen(target))
		}
		if got := decodeSL(b[4:n]); got != target {
			t.Errorf("%q round-tripped to %q", target, got)
		}
	}
}
