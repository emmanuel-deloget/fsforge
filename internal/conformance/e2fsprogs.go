package conformance

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ErrUnavailable means no e2fsprogs checker could be located, on the host or via
// a container runtime. Tests should skip on this.
var ErrUnavailable = errors.New("conformance: no e2fsprogs available (host or container)")

// e2fsprogsImage is the container image used when e2fsck is not on the host. It
// must provide e2fsck (alpine pulls it on demand via apk). Override with
// FSFORGE_E2FSPROGS_IMAGE to use a pinned image that already ships e2fsprogs.
func e2fsprogsImage() string {
	if v := os.Getenv("FSFORGE_E2FSPROGS_IMAGE"); v != "" {
		return v
	}
	return "alpine"
}

// E2fsck runs `e2fsck -fn` on imagePath and returns its combined output. A nil
// error means the image is clean (exit 0); a non-nil error other than
// ErrUnavailable means e2fsck reported problems.
func E2fsck(imagePath string) (string, error) {
	return runTool(imagePath, "e2fsck", "-fn")
}

// Dumpe2fs returns `dumpe2fs -h` output for imagePath, for diagnostics.
func Dumpe2fs(imagePath string) (string, error) {
	return runTool(imagePath, "dumpe2fs", "-h")
}

func runTool(imagePath, tool string, args ...string) (string, error) {
	if host, err := exec.LookPath(tool); err == nil {
		out, err := exec.Command(host, append(args, imagePath)...).CombinedOutput()
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
	script := fmt.Sprintf("command -v %s >/dev/null 2>&1 || apk add -q e2fsprogs >/dev/null 2>&1; %s %s /work/%s",
		tool, tool, shellJoin(args), base)
	cmd := exec.Command(runtime, "run", "--rm", "-v", dir+":/work:Z", e2fsprogsImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func containerRuntime() string {
	for _, r := range []string{"podman", "docker"} {
		if _, err := exec.LookPath(r); err == nil {
			return r
		}
	}
	return ""
}

// ContainerRuntime returns the available container runtime ("podman"/"docker")
// or "" if none, for conformance tests that drive a runtime directly.
func ContainerRuntime() string { return containerRuntime() }

func shellJoin(args []string) string {
	s := ""
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}
