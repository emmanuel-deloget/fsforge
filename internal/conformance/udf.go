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

func p7zipImage() string {
	if v := os.Getenv("FSFORGE_P7ZIP_IMAGE"); v != "" {
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

// Udf7zExtract extracts imagePath into destPath with 7-Zip's independent UDF
// reader (host 7z or a container), returning the combined output. destPath must
// share a directory with imagePath for the container path. ErrUnavailable means
// no 7z was found. 7-Zip aborts on UDF symlinks and device nodes, so callers
// extract a sample of regular files and directories only.
func Udf7zExtract(imagePath, destPath string) (string, error) {
	if host, err := exec.LookPath("7z"); err == nil {
		out, err := exec.Command(host, "x", "-y", "-o"+destPath, imagePath).CombinedOutput()
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
		"command -v 7z >/dev/null 2>&1 || apk add -q 7zip >/dev/null 2>&1; 7z x -y -o/work/%s /work/%s",
		filepath.Base(destPath), filepath.Base(imagePath))
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", p7zipImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
