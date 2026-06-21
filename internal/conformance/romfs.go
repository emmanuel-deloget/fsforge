package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// genromfsImage is the container image used when genromfs is not on the host.
// genromfs lives in Debian's repositories, not Alpine's, so the default is a
// Debian image. Override with FSFORGE_GENROMFS_IMAGE.
func genromfsImage() string {
	if v := os.Getenv("FSFORGE_GENROMFS_IMAGE"); v != "" {
		return v
	}
	return "debian:stable-slim"
}

// MakeRomfs builds a romfs image at imagePath from the contents of srcDir using
// the real genromfs (host or container), so the reader can be checked against an
// independent writer. srcDir and imagePath must share a directory for the
// container path. ErrUnavailable means no genromfs was found.
func MakeRomfs(srcDir, imagePath string) (string, error) {
	if host, err := exec.LookPath("genromfs"); err == nil {
		out, err := exec.Command(host, "-d", srcDir, "-f", imagePath, "-V", "fsforge").CombinedOutput()
		return string(out), err
	}
	runtime := containerRuntime()
	if runtime == "" {
		return "", ErrUnavailable
	}
	dir, err := filepath.Abs(filepath.Dir(imagePath))
	if err != nil {
		return "", err
	}
	src, err := filepath.Abs(srcDir)
	if err != nil {
		return "", err
	}
	if d, _ := filepath.Abs(filepath.Dir(src)); d != dir {
		return "", fmt.Errorf("conformance: image and source must share a directory")
	}
	script := fmt.Sprintf(
		"export DEBIAN_FRONTEND=noninteractive; "+
			"command -v genromfs >/dev/null 2>&1 || { apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq genromfs >/dev/null 2>&1; }; "+
			"genromfs -d /work/%s -f /work/%s -V fsforge",
		filepath.Base(src), filepath.Base(imagePath))
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", genromfsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
