//go:build !linux

package main

// cpuModel has no portable source outside Linux; the chart falls back to the
// architecture and core count, which is worse but not wrong.
func cpuModel() string { return "" }
