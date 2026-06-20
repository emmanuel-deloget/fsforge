package conformance

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

// Sfdisk runs `sfdisk -l` on imagePath (host or container with util-linux),
// returning its listing. ErrUnavailable means no tool was found.
func Sfdisk(imagePath string) (string, error) {
	if host, err := exec.LookPath("sfdisk"); err == nil {
		out, err := exec.Command(host, "-l", imagePath).CombinedOutput()
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
		"command -v sfdisk >/dev/null 2>&1 || apk add -q util-linux >/dev/null 2>&1; sfdisk -l /work/%s", base)
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", e2fsprogsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
