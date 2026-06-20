package conformance

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

// XorrisoExtract extracts the ISO at imagePath into destPath (same parent dir),
// honouring Rock Ridge, via a host xorriso or a container runtime. ErrUnavailable
// means no tool was found.
func XorrisoExtract(imagePath, destPath string) (string, error) {
	if host, err := exec.LookPath("xorriso"); err == nil {
		out, err := exec.Command(host, "-osirrox", "on", "-indev", imagePath, "-extract", "/", destPath).CombinedOutput()
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
		"command -v xorriso >/dev/null 2>&1 || apk add -q xorriso >/dev/null 2>&1; "+
			"xorriso -osirrox on -indev /work/%s -extract / /work/%s",
		filepath.Base(imagePath), filepath.Base(destPath))
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", e2fsprogsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
