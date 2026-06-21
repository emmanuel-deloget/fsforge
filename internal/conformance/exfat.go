package conformance

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// FsckExFAT runs `fsck.exfat -n` on imagePath (host or container with
// exfatprogs) and returns its output. Because fsck.exfat may exit 0 even when it
// reports problems, callers should also inspect the text (see CheckExFATClean).
func FsckExFAT(imagePath string) (string, error) {
	if host, err := exec.LookPath("fsck.exfat"); err == nil {
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
		"command -v fsck.exfat >/dev/null 2>&1 || apk add -q exfatprogs >/dev/null 2>&1; fsck.exfat -n /work/%s", base)
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", e2fsprogsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// CheckExFATClean reports whether fsck.exfat output indicates a clean volume.
func CheckExFATClean(out string) bool {
	return strings.Contains(out, "clean") &&
		!strings.Contains(out, "corrupt") &&
		!strings.Contains(out, "ERROR")
}
