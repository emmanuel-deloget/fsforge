package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// qemuImage is the container image used when qemu-img is not on the host.
// Override with FSFORGE_QEMU_IMAGE.
func qemuImage() string {
	if v := os.Getenv("FSFORGE_QEMU_IMAGE"); v != "" {
		return v
	}
	return "alpine"
}

// QemuImgCheck runs `qemu-img check` on a QCOW2 image (host or container with
// qemu-img), returning the combined output. A nil error means the image checked
// out clean. ErrUnavailable means no qemu-img was found.
func QemuImgCheck(imagePath string) (string, error) {
	return qemuImg(filepath.Dir(imagePath), "check", "/work/"+filepath.Base(imagePath))
}

// QemuImgToRaw converts a QCOW2 image to a raw file with the real qemu-img.
// imagePath and rawPath must share a directory. ErrUnavailable means no
// qemu-img was found.
func QemuImgToRaw(imagePath, rawPath string) (string, error) {
	if d1, d2 := filepath.Dir(imagePath), filepath.Dir(rawPath); d1 != d2 {
		return "", fmt.Errorf("conformance: image and raw must share a directory")
	}
	return qemuImg(filepath.Dir(imagePath), "convert", "-O", "raw",
		"/work/"+filepath.Base(imagePath), "/work/"+filepath.Base(rawPath))
}

// MakeQcow2FromRaw builds a QCOW2 image from a raw file with the real qemu-img,
// for the reader round-trip. rawPath and imagePath must share a directory.
func MakeQcow2FromRaw(rawPath, imagePath string) (string, error) {
	if d1, d2 := filepath.Dir(rawPath), filepath.Dir(imagePath); d1 != d2 {
		return "", fmt.Errorf("conformance: raw and image must share a directory")
	}
	return qemuImg(filepath.Dir(rawPath), "convert", "-f", "raw", "-O", "qcow2",
		"/work/"+filepath.Base(rawPath), "/work/"+filepath.Base(imagePath))
}

// qemuImg runs a qemu-img subcommand whose path arguments are expressed relative
// to /work (the mounted dir) so the same args work on host and in a container.
func qemuImg(workDir string, args ...string) (string, error) {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", err
	}
	if host, err := exec.LookPath("qemu-img"); err == nil {
		hostArgs := make([]string, len(args))
		for i, a := range args {
			hostArgs[i] = stripWork(a, abs)
		}
		out, err := exec.Command(host, hostArgs...).CombinedOutput()
		return string(out), err
	}

	runtime := containerRuntime()
	if runtime == "" {
		return "", ErrUnavailable
	}
	script := "command -v qemu-img >/dev/null 2>&1 || apk add -q qemu-img >/dev/null 2>&1; qemu-img " + shellJoin(args)
	cmd := exec.Command(runtime, "run", "--rm", "-v", abs+":/work:Z", qemuImage(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// stripWork rewrites a "/work/<base>" argument to an absolute host path.
func stripWork(arg, abs string) string {
	const p = "/work/"
	if len(arg) > len(p) && arg[:len(p)] == p {
		return filepath.Join(abs, arg[len(p):])
	}
	return arg
}
