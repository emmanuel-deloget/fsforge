package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cpioImage is the container image used when cpio is not on the host. Override
// with FSFORGE_CPIO_IMAGE.
func cpioImage() string {
	if v := os.Getenv("FSFORGE_CPIO_IMAGE"); v != "" {
		return v
	}
	return "alpine"
}

// CpioExtract unpacks the newc archive at archivePath into destDir using the
// real GNU cpio (host or container), returning the combined output. destDir is
// created if needed and, for the container path, must share a directory with
// archivePath. ErrUnavailable means no cpio was found.
func CpioExtract(archivePath, destDir string) (string, error) {
	if host, err := exec.LookPath("cpio"); err == nil {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return "", err
		}
		f, err := os.Open(archivePath)
		if err != nil {
			return "", err
		}
		defer f.Close()
		cmd := exec.Command(host, "-i", "-d", "-m", "--no-absolute-filenames")
		cmd.Dir = destDir
		cmd.Stdin = f
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	runtime := containerRuntime()
	if runtime == "" {
		return "", ErrUnavailable
	}
	dir, err := filepath.Abs(filepath.Dir(archivePath))
	if err != nil {
		return "", err
	}
	if d, _ := filepath.Abs(filepath.Dir(destDir)); d != dir {
		return "", fmt.Errorf("conformance: archive and dest must share a directory")
	}
	script := fmt.Sprintf(
		"command -v cpio >/dev/null 2>&1 || apk add -q cpio >/dev/null 2>&1; "+
			"mkdir -p /work/%s && cd /work/%s && cpio -i -d -m --no-absolute-filenames < /work/%s",
		filepath.Base(destDir), filepath.Base(destDir), filepath.Base(archivePath))
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", cpioImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// MakeCpio builds a newc archive at archivePath from the contents of srcDir
// using the real GNU cpio (host or container). ErrUnavailable means no cpio was
// found.
func MakeCpio(srcDir, archivePath string) (string, error) {
	if _, err := exec.LookPath("cpio"); err == nil {
		out, err := os.Create(archivePath)
		if err != nil {
			return "", err
		}
		defer out.Close()
		var stderr strings.Builder
		cmd := exec.Command("sh", "-c", "find . -print0 | cpio -0 -o -H newc")
		cmd.Dir = srcDir
		cmd.Stdout = out
		cmd.Stderr = &stderr
		err = cmd.Run()
		return stderr.String(), err
	}

	runtime := containerRuntime()
	if runtime == "" {
		return "", ErrUnavailable
	}
	dir, err := filepath.Abs(filepath.Dir(archivePath))
	if err != nil {
		return "", err
	}
	src, err := filepath.Abs(srcDir)
	if err != nil {
		return "", err
	}
	if d, _ := filepath.Abs(filepath.Dir(src)); d != dir {
		return "", fmt.Errorf("conformance: archive and source must share a directory")
	}
	script := fmt.Sprintf(
		"command -v cpio >/dev/null 2>&1 || apk add -q cpio >/dev/null 2>&1; "+
			"cd /work/%s && find . -print0 | cpio -0 -o -H newc > /work/%s",
		filepath.Base(src), filepath.Base(archivePath))
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", cpioImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
