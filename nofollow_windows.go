//go:build windows

package fsforge

// oNoFollow has no Windows equivalent reachable from the standard library:
// O_NOFOLLOW is a POSIX open flag, and the Win32 equivalent
// (FILE_FLAG_OPEN_REPARSE_POINT) is not exposed by os.OpenFile. Extraction on
// Windows therefore relies on name validation alone, which is what stops a
// crafted image from placing a link in the path to begin with. Creating a
// symlink on Windows also needs a privilege the unprivileged case lacks.
const oNoFollow = 0
