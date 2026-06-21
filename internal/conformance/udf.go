package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func udftoolsImage() string {
	if v := os.Getenv("FSFORGE_UDFTOOLS_IMAGE"); v != "" {
		return v
	}
	return "alpine"
}

// UdfInfo runs udfinfo (udftools) on imagePath, on the host or in a container,
// and returns its output. A non-ErrUnavailable error means udfinfo failed to
// parse the image.
func UdfInfo(imagePath string) (string, error) {
	if host, err := exec.LookPath("udfinfo"); err == nil {
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
	script := fmt.Sprintf(
		"command -v udfinfo >/dev/null 2>&1 || apk add -q udftools >/dev/null 2>&1; udfinfo /work/%s",
		filepath.Base(imagePath))
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", udftoolsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
