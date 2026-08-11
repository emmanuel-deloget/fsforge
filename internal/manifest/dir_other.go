//go:build !unix

package manifest

import "errors"

// FromDir needs Lstat's Unix metadata — owner, link count, device number — which
// this platform does not expose through os.FileInfo.Sys(). It is only ever
// called from conformance tests, which run on Linux; the stub keeps the package
// buildable everywhere rather than pushing build tags out to every caller.
func FromDir(string) (Manifest, error) {
	return nil, errors.New("manifest: FromDir is only implemented on unix")
}
