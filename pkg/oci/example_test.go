package oci_test

import (
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/oci"
	"github.com/emmanuel-deloget/fsforge/pkg/tree"
)

func exampleDeps() image.Deps {
	return image.Deps{Clock: image.FixedClock{T: time.Unix(0, 0)}, UUID: image.FixedUUID{}}
}

func childNames(n *image.Node) []string {
	var names []string
	for _, e := range n.Children {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return names
}

// Stack a second tree onto an image as an additive layer: the new file joins the
// existing ones, nothing is removed.
func ExampleAddLayer() {
	dir, _ := os.MkdirTemp("", "oci")
	defer os.RemoveAll(dir)
	l, _ := oci.CreateLayout(dir)
	deps := exampleDeps()

	base := image.NewMem(deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	base.Root().Create("app", tree.Bytes("v1"), tree.Meta{Mode: 0o755})
	oci.Build(l, base, oci.BuildOptions{Ref: "app:latest"})

	top := image.NewMem(deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	top.Root().Create("config.yaml", tree.Bytes("debug: true\n"), tree.Meta{Mode: 0o644})
	oci.AddLayer(l, "app:latest", top, oci.BuildOptions{Ref: "app:latest"})

	img, _, cleanup, _ := oci.Flatten(l, "app:latest", deps)
	defer cleanup()
	fmt.Println(childNames(img.RootNode()))
	// Output: [app config.yaml]
}

// Stack a delta layer: the new tree is the desired end state, and AddLayerDiff
// records the difference — adding new.txt and whiteing out the removed old.txt.
func ExampleAddLayerDiff() {
	dir, _ := os.MkdirTemp("", "oci")
	defer os.RemoveAll(dir)
	l, _ := oci.CreateLayout(dir)
	deps := exampleDeps()

	base := image.NewMem(deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	base.Root().Create("keep.txt", tree.Bytes("keep\n"), tree.Meta{Mode: 0o644})
	base.Root().Create("old.txt", tree.Bytes("old\n"), tree.Meta{Mode: 0o644})
	oci.Build(l, base, oci.BuildOptions{Ref: "app:latest"})

	next := image.NewMem(deps, tree.Meta{Mode: fs.ModeDir | 0o755})
	next.Root().Create("keep.txt", tree.Bytes("keep\n"), tree.Meta{Mode: 0o644})
	next.Root().Create("new.txt", tree.Bytes("new\n"), tree.Meta{Mode: 0o644})
	oci.AddLayerDiff(l, "app:latest", next, oci.BuildOptions{Ref: "app:latest"})

	img, _, cleanup, _ := oci.Flatten(l, "app:latest", deps)
	defer cleanup()
	fmt.Println(childNames(img.RootNode()))
	// Output: [keep.txt new.txt]
}
