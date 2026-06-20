package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// squashfsImage is the container image used when unsquashfs is not on the host.
// Override with FSFORGE_SQUASHFS_IMAGE to use a pinned image that already ships
// squashfs-tools.
func squashfsImage() string {
	if v := os.Getenv("FSFORGE_SQUASHFS_IMAGE"); v != "" {
		return v
	}
	return "alpine"
}

// Unsquashfs extracts imagePath into destPath (which must not already exist and
// must live in the same directory as imagePath) and returns the combined
// output. It uses a host unsquashfs if present, otherwise a container runtime.
// ErrUnavailable means neither was found.
func Unsquashfs(imagePath, destPath string) (string, error) {
	if host, err := exec.LookPath("unsquashfs"); err == nil {
		out, err := exec.Command(host, "-d", destPath, "-no-xattrs", imagePath).CombinedOutput()
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
	if d, _ := filepath.Abs(filepath.Dir(destPath)); d != dir {
		return "", fmt.Errorf("conformance: image and dest must share a directory")
	}
	script := fmt.Sprintf(
		"command -v unsquashfs >/dev/null 2>&1 || apk add -q squashfs-tools >/dev/null 2>&1; "+
			"unsquashfs -d /work/%s -no-xattrs /work/%s",
		filepath.Base(destPath), filepath.Base(imagePath))
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", squashfsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
