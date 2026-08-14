package fsforge_test

import (
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	fsforge "github.com/emmanuel-deloget/fsforge"
)

// TestStreamingKeepsMemoryBounded turns a README claim into a check: image size
// is not bounded by RAM, because contents flow through tree.Source and are
// streamed at finalize rather than held.
//
// A claim like that decays quietly. Somebody adds an io.ReadAll to fix a bug,
// every test still passes, and the property is gone until an image large enough
// to matter meets a machine small enough to notice — usually somebody else's.
// Sampling the heap while a large image is written is what makes the regression
// arrive as a failing test instead.
func TestStreamingKeepsMemoryBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a multi-gigabyte sparse file")
	}
	const (
		fileSize = 2 << 30   // two gigabytes of content, in one file
		ceiling  = 256 << 20 // and a heap that must stay far below it
	)

	src := t.TempDir()
	f, err := os.Create(filepath.Join(src, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: the bytes are zeroes the filesystem never stores, so the test
	// costs no disk while still handing the engine two gigabytes to read.
	if err := f.Truncate(fileSize); err != nil {
		t.Skipf("cannot make a sparse file here: %v", err)
	}
	f.Close()

	peak := sampleHeap(t)
	out := filepath.Join(t.TempDir(), "big.erofs")
	if err := fsforge.New("erofs").Reproducible(1600000000).
		BuildFromDir(src, out); err != nil {
		t.Fatalf("build: %v", err)
	}
	got := peak()

	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() < fileSize {
		t.Fatalf("image is %d bytes for %d of content; it did not write the file",
			st.Size(), int64(fileSize))
	}
	if got > ceiling {
		t.Errorf("peak heap %d MiB while writing a %d MiB image; contents are being held, not streamed",
			got>>20, st.Size()>>20)
	}
	// A reading of zero would mean the sampler never ran, which would make the
	// check above pass for the wrong reason.
	if got == 0 {
		t.Error("the heap sampler reported nothing; the measurement is not running")
	}
	t.Logf("peak heap %d KiB for a %d MiB image", got>>10, st.Size()>>20)
}

// sampleHeap watches the heap until the returned function is called, which
// stops it and reports the highest reading. Sampling beats a before-and-after
// pair: a buffer that is allocated and released inside the build would leave no
// trace in the latter.
func sampleHeap(t *testing.T) func() uint64 {
	t.Helper()
	var high atomic.Uint64
	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		var ms runtime.MemStats
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				runtime.ReadMemStats(&ms)
				for {
					cur := high.Load()
					if ms.HeapAlloc <= cur || high.CompareAndSwap(cur, ms.HeapAlloc) {
						break
					}
				}
			}
		}
	}()

	return func() uint64 {
		close(done)
		<-stopped
		return high.Load()
	}
}
