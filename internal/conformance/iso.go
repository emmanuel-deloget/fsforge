package conformance

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

// MakeISO builds a Rock Ridge ISO9660 image at outPath from the directory
// srcDir using the real xorriso (as mkisofs), on the host or via a container.
// srcDir and outPath must share a parent directory (the one mounted into the
// container). ErrUnavailable means no tool was found.
func MakeISO(srcDir, outPath string) (string, error) {
	if host, err := exec.LookPath("xorriso"); err == nil {
		out, err := exec.Command(host, "-as", "mkisofs", "-R", "-o", outPath, srcDir).CombinedOutput()
		return string(out), err
	}
	runtime := containerRuntime()
	if runtime == "" {
		return "", ErrUnavailable
	}
	parent, err := filepath.Abs(filepath.Dir(outPath))
	if err != nil {
		return "", err
	}
	if d, _ := filepath.Abs(filepath.Dir(srcDir)); d != parent {
		return "", fmt.Errorf("conformance: srcDir and outPath must share a directory")
	}
	script := fmt.Sprintf(
		"command -v xorriso >/dev/null 2>&1 || apk add -q xorriso >/dev/null 2>&1; "+
			"xorriso -as mkisofs -R -o /work/%s /work/%s",
		filepath.Base(outPath), filepath.Base(srcDir))
	cmd := exec.Command(runtime, "run", "--rm", "-v", parent+":/work:Z", e2fsprogsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

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
