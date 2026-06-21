package device_test

import (
	"fmt"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// Mem is the in-memory backend engines and tests run against; Section carves a
// sub-window of any device, the way a partition is handed to an engine.
func ExampleSection() {
	disk := device.NewMem(64)
	part := device.NewSection(disk, 16, 32) // [16,48) of the disk

	part.WriteAt([]byte("forge"), 0)

	// The write landed at absolute offset 16 in the underlying disk.
	p := make([]byte, 5)
	disk.ReadAt(p, 16)
	fmt.Println(string(p))
	// Output: forge
}
