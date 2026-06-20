package device

import "os"

// File adapts an *os.File to Device with a fixed logical size. The host file is
// the production backend; *os.File already provides ReadAt/WriteAt, so this is
// only a thin size-carrying wrapper.
type File struct {
	*os.File
	size int64
}

// NewFile wraps f, reporting size as the logical device size. The caller owns
// f's lifecycle (including truncation to size and Close).
func NewFile(f *os.File, size int64) *File {
	return &File{File: f, size: size}
}

func (f *File) Size() int64 { return f.size }
