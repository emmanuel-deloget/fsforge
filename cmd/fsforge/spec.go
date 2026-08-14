package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/emmanuel-deloget/fsforge/pkg/mtree"
)

// specCmd writes an mtree description of a directory, which is the starting
// point for one: generate, edit the ownership and the device nodes in, keep it
// next to the tree it describes.
func specCmd(args []string) error {
	fsSet := flag.NewFlagSet("spec", flag.ContinueOnError)
	source := fsSet.String("source", "", "directory to describe")
	output := fsSet.String("output", "", "output file (default: stdout)")
	if err := fsSet.Parse(args); err != nil {
		return err
	}
	if *source == "" {
		return fmt.Errorf("-source is required")
	}

	w := os.Stdout
	if *output != "" {
		f, err := os.Create(*output)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if err := mtree.FromDir(w, *source); err != nil {
		return err
	}
	if *output != "" {
		fmt.Printf("wrote spec %s\n", *output)
	}
	return nil
}
