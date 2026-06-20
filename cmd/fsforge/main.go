// Command fsforge is a thin CLI over the fsforge library. It carries no
// business logic of its own: its job is to turn a declarative manifest into a
// filesystem image (reproducibly) and to drive the library for ad-hoc image
// creation, inspection and offline mutation.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fsforge:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// Subcommands (mkfs, ls, add, rm, …) are wired here as engines land.
	fmt.Println("fsforge: pure-Go filesystem image builder (scaffolding)")
	return nil
}
