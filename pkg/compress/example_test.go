package compress_test

import (
	"fmt"

	"github.com/emmanuel-deloget/fsforge/pkg/compress"
)

// Codecs compress and decompress independent blocks, appending to the caller's
// buffer so engines control allocation.
func ExampleGzip() {
	var c compress.Gzip
	packed, _ := c.Compress(nil, []byte("fsforge fsforge fsforge"))
	out, _ := c.Decompress(nil, packed)

	fmt.Println(string(out))
	// Output: fsforge fsforge fsforge
}
