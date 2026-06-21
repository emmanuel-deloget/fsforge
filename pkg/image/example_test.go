package image_test

import (
	"fmt"
	"io/fs"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

// Edit an in-memory image tree through the Dir surface. Wiring a FixedClock and
// FixedUUID is all it takes to make a build reproducible — there is no flag.
func ExampleNewMem() {
	deps := image.Deps{Clock: image.FixedClock{T: time.Unix(0, 0)}, UUID: image.FixedUUID{}}
	img := image.NewMem(deps, tree.Meta{Mode: fs.ModeDir | 0o755})

	root := img.Root()
	root.Mkdir("etc", tree.Meta{Mode: 0o755})
	root.Create("readme", tree.Bytes("hello"), tree.Meta{Mode: 0o644})

	var names []string
	root.Range(func(d tree.Dirent) error {
		names = append(names, d.Name)
		return nil
	})
	fmt.Println(names)
	// Output: [etc readme]
}
