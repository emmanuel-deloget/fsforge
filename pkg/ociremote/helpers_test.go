package ociremote

import (
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

func testDeps() image.Deps {
	return image.Deps{
		Clock: image.FixedClock{T: time.Unix(1_600_000_000, 0).UTC()},
		UUID:  image.FixedUUID{},
	}
}

func childNamed(n *image.Node, name string) *image.Node {
	for _, e := range n.Children {
		if e.Name == name {
			return e.Node
		}
	}
	return nil
}
