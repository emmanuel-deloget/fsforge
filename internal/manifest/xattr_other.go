//go:build unix && !linux

package manifest

import "io/fs"

// readXattrs is a no-op outside Linux: the syscalls differ per BSD, and the
// conformance tools that produce directories to compare only run on Linux.
func readXattrs(string, fs.FileMode) (map[string][]byte, error) { return nil, nil }
