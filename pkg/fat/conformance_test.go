//go:build conformance

package fat

import (
	"errors"
	"os"
	"testing"

	"github.com/emmanuel-deloget/fsforge/internal/conformance"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// TestFsckFATConformance builds FAT12/16/32 images and checks each with the real
// fsck.fat (host or container). Run: go test -tags conformance ./pkg/fat/
func TestFsckFATConformance(t *testing.T) {
	cases := []struct {
		name string
		bits int
		size int64
	}{
		{"FAT12", 12, 2 << 20},
		{"FAT16", 16, 32 << 20},
		{"FAT32", 32, 64 << 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "fsforge-*.fat")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := f.Truncate(c.size); err != nil {
				t.Fatal(err)
			}
			buildSampleWith(t, New(testDeps(), WithFATBits(c.bits)), device.NewFile(f, c.size))
			if err := f.Sync(); err != nil {
				t.Fatal(err)
			}

			out, err := conformance.FsckFAT(f.Name())
			if errors.Is(err, conformance.ErrUnavailable) {
				t.Skip("fsck.fat unavailable")
			}
			if err != nil {
				t.Fatalf("fsck.fat reported problems: %v\n%s", err, out)
			}
			t.Logf("%s clean:\n%s", c.name, out)
		})
	}
}
