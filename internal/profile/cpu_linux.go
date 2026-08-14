//go:build linux

package main

import (
	"os"
	"strings"
)

// cpuModel reads the processor's name, so a reader knows what the timings are
// worth. "12 cores" says nothing on its own: the same core count spans a decade
// of hardware.
func cpuModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if name, val, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(name) == "model name" {
			return tidyCPU(strings.TrimSpace(val))
		}
	}
	return ""
}
