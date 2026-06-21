package fsforge_test

import (
	"fmt"
	"os"
	"path/filepath"

	fsforge "github.com/emmanuel-deloget/fsforge"
)

// Build a reproducible ext4 image from a directory in a few lines.
func ExampleBuilder_BuildFromDir() {
	src, _ := os.MkdirTemp("", "src")
	defer os.RemoveAll(src)
	os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hi"), 0o644)

	out := filepath.Join(os.TempDir(), "example-root.img")
	defer os.Remove(out)

	err := fsforge.New("ext4").
		Reproducible(fsforge.SourceDateEpoch()).
		Size("16M").
		Label("root").
		BuildFromDir(src, out)

	fmt.Println("built:", err == nil)
	// Output: built: true
}

// Convert a directory into a squashfs archive through the shared tree model.
func ExampleConvert() {
	src, _ := os.MkdirTemp("", "src")
	defer os.RemoveAll(src)
	os.WriteFile(filepath.Join(src, "app"), []byte("binary"), 0o755)

	out := filepath.Join(os.TempDir(), "example.sqfs")
	defer os.Remove(out)

	err := fsforge.Convert(
		fsforge.Location{Kind: "dir", Path: src},
		fsforge.Location{Kind: "squashfs", Path: out},
		fsforge.Options{},
	)

	fmt.Println("converted:", err == nil)
	// Output: converted: true
}
