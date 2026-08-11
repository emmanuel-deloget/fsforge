//go:build linux

package manifest

import "syscall"

func setxattr(path, name string, value []byte) error {
	return syscall.Setxattr(path, name, value, 0)
}
