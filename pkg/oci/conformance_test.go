//go:build conformance

package oci

import (
	"archive/tar"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
)

// TestPodmanRoundTrip builds an OCI image with fsforge, has the real container
// runtime pull it, exports the resulting rootfs and checks our content survived.
// Run with: go test -tags conformance ./pkg/oci/
func TestPodmanRoundTrip(t *testing.T) {
	rt := conformance.ContainerRuntime()
	if rt == "" {
		t.Skip("no container runtime (podman/docker)")
	}

	// A lowercase path: container image references reject uppercase, and the
	// oci: transport embeds the layout path in the reference.
	dir, err := os.MkdirTemp("", "fsforge-oci-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := CreateLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(l, sampleImage(t), BuildOptions{
		Ref:    "fsforge:conf",
		Gzip:   true,
		Config: RunConfig{Cmd: []string{"/bin/sh"}},
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	ref := "oci:" + dir + ":fsforge:conf"
	if out, err := exec.Command(rt, "pull", ref).CombinedOutput(); err != nil {
		t.Fatalf("%s pull failed: %v\n%s", rt, err, out)
	}
	defer exec.Command(rt, "rmi", "-f", ref).Run()

	cidRaw, err := exec.Command(rt, "create", ref).CombinedOutput()
	if err != nil {
		t.Fatalf("%s create failed: %v\n%s", rt, err, cidRaw)
	}
	cid := lastLine(string(cidRaw)) // create also prints copy-progress lines
	defer exec.Command(rt, "rm", cid).Run()

	tarPath := filepath.Join(dir, "rootfs.tar")
	if out, err := exec.Command(rt, "export", cid, "-o", tarPath).CombinedOutput(); err != nil {
		t.Fatalf("%s export failed: %v\n%s", rt, err, out)
	}

	got := tarFile(t, tarPath, "etc/hosts")
	if got != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts via %s = %q", rt, got)
	}
	t.Logf("%s accepted fsforge image; rootfs content verified", rt)
}

// TestPodmanFlattenAlpine exports a real alpine image via the runtime and
// flattens it with fsforge, checking a known file. Skips if the runtime or the
// image (and network to fetch it) is unavailable.
func TestPodmanFlattenAlpine(t *testing.T) {
	rt := conformance.ContainerRuntime()
	if rt == "" {
		t.Skip("no container runtime")
	}
	if out, err := exec.Command(rt, "pull", "alpine:latest").CombinedOutput(); err != nil {
		t.Skipf("cannot obtain alpine image: %v\n%s", err, out)
	}

	dir, err := os.MkdirTemp("", "fsforge-alpine-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	layoutDir := filepath.Join(dir, "oci")
	if out, err := exec.Command(rt, "save", "--format", "oci-dir", "-o", layoutDir, "alpine:latest").CombinedOutput(); err != nil {
		t.Fatalf("%s save failed: %v\n%s", rt, err, out)
	}

	l, err := OpenLayout(layoutDir)
	if err != nil {
		t.Fatal(err)
	}
	mem, cfg, cleanup, err := Flatten(l, "", testDeps())
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	defer cleanup()

	if cfg.OS != "linux" {
		t.Errorf("os = %q", cfg.OS)
	}
	etc := child(mem.RootNode(), "etc")
	if etc == nil {
		t.Fatal("no /etc in flattened alpine")
	}
	rel := child(etc, "alpine-release")
	if rel == nil || rel.Content == nil {
		t.Fatal("no /etc/alpine-release")
	}
	if v := string(readSource(t, rel.Content)); len(v) == 0 || v[0] < '0' || v[0] > '9' {
		t.Errorf("alpine-release looks wrong: %q", v)
	}
	t.Logf("flattened real alpine: /etc/alpine-release ok, %d top-level entries", len(mem.RootNode().Children))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func tarFile(t *testing.T, tarPath, name string) string {
	t.Helper()
	f, err := os.Open(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("%s not found in rootfs", name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimPrefix(hdr.Name, "./") == name {
			b, _ := io.ReadAll(tr)
			return string(b)
		}
	}
}
