package conformance

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

// FsckFAT runs `fsck.fat -n` on imagePath, using a host binary if present and
// otherwise a container runtime (pulling dosfstools on demand). A nil error
// means the volume is clean; ErrUnavailable means no checker was found.
func FsckFAT(imagePath string) (string, error) {
	if host, err := exec.LookPath("fsck.fat"); err == nil {
		out, err := exec.Command(host, "-n", imagePath).CombinedOutput()
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
		"command -v fsck.fat >/dev/null 2>&1 || apk add -q dosfstools >/dev/null 2>&1; fsck.fat -n /work/%s", base)
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", e2fsprogsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
