package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// erofsImage is the container image used when erofs-utils is not on the host.
// Override with FSFORGE_EROFS_IMAGE to use a pinned image that already ships
// mkfs.erofs/fsck.erofs.
func erofsImage() string {
	if v := os.Getenv("FSFORGE_EROFS_IMAGE"); v != "" {
		return v
	}
	return "alpine"
}

// FsckErofs runs `fsck.erofs` on imagePath (host or container with erofs-utils)
// and returns the combined output. A nil error means the image is structurally
// clean (exit 0); ErrUnavailable means no tool was found.
func FsckErofs(imagePath string) (string, error) {
	if host, err := exec.LookPath("fsck.erofs"); err == nil {
		out, err := exec.Command(host, imagePath).CombinedOutput()
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
	base := filepath.Base(imagePath)
	script := fmt.Sprintf(
		"command -v fsck.erofs >/dev/null 2>&1 || apk add -q erofs-utils >/dev/null 2>&1; fsck.erofs /work/%s", base)
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", erofsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ErofsExtract extracts imagePath into destPath using `fsck.erofs --extract`
// (host or container). destPath must not already exist and must share a
// directory with imagePath. ErrUnavailable means no tool was found.
func ErofsExtract(imagePath, destPath string) (string, error) {
	if host, err := exec.LookPath("fsck.erofs"); err == nil {
		out, err := exec.Command(host, "--extract="+destPath, imagePath).CombinedOutput()
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
		"command -v fsck.erofs >/dev/null 2>&1 || apk add -q erofs-utils >/dev/null 2>&1; "+
			"fsck.erofs --extract=/work/%s /work/%s",
		filepath.Base(destPath), filepath.Base(imagePath))
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", erofsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// MakeErofs builds an EROFS image at imagePath from srcDir using the real
// mkfs.erofs (host or container). ErrUnavailable means no tool was found.
func MakeErofs(srcDir, imagePath string) (string, error) {
	if host, err := exec.LookPath("mkfs.erofs"); err == nil {
		out, err := exec.Command(host, imagePath, srcDir).CombinedOutput()
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
		"command -v mkfs.erofs >/dev/null 2>&1 || apk add -q erofs-utils >/dev/null 2>&1; "+
			"mkfs.erofs /work/%s /work/%s",
		filepath.Base(imagePath), filepath.Base(src))
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", erofsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
