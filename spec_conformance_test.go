//go:build conformance

package fsforge_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/internal/conformance"
)

// TestSpecRootfsConformance builds the rootfs an unprivileged checkout plus an
// mtree spec produce, and puts it in front of e2fsck. Device nodes and setuid
// bits are exactly the metadata a filesystem check looks hardest at, and they
// are precisely what the spec — rather than the source tree — supplied.
func TestSpecRootfsConformance(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "ping"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(t.TempDir(), "rootfs.mtree")
	if err := os.WriteFile(specPath, []byte(`/set uid=0 gid=0
./bin          type=dir  mode=0755
./bin/ping     type=file mode=4755
./dev          type=dir  mode=0755
./dev/console  type=char mode=0600 device=native,5,1
./dev/null     type=char mode=0666 device=native,1,3
./dev/tty      type=char mode=0666 device=native,5,0
./tmp          type=dir  mode=01777
./var/run      type=link link=../run
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "rootfs.img")
	if err := fsforge.New("ext4").Reproducible(1600000000).Size("16M").
		Label("root").Spec(specPath).BuildFromDir(src, out); err != nil {
		t.Fatalf("build: %v", err)
	}

	res, err := conformance.E2fsck(out)
	if errors.Is(err, conformance.ErrUnavailable) {
		t.Skip("e2fsprogs unavailable (no host binary or container runtime)")
	}
	if err != nil {
		t.Fatalf("e2fsck reported problems: %v\n%s", err, res)
	}
}
