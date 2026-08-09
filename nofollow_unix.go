//go:build !windows

package fsforge

import "syscall"

// oNoFollow makes an open fail when the final path component is a symlink, so
// extraction never writes through a link into whatever it names.
const oNoFollow = syscall.O_NOFOLLOW
