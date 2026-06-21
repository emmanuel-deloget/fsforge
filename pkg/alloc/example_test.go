package alloc_test

import (
	"fmt"

	"github.com/emmanuel-deloget/fsforge/pkg/alloc"
)

// The bitmap allocator is deterministic first-fit: a given sequence of calls
// always yields the same starts, which is what makes image layout reproducible.
func ExampleBitmap() {
	b := alloc.NewBitmap(64)
	first, _ := b.Alloc(4)  // blocks [0,4)
	second, _ := b.Alloc(2) // blocks [4,6)
	b.Free(first, 4)
	reused, _ := b.Alloc(4) // first-fit reuses [0,4)

	fmt.Println(first, second, reused)
	// Output: 0 4 0
}
