package main

import (
	"runtime"
	"runtime/metrics"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
)

// The two memory readings answer different questions, and only one of them is
// the one worth plotting.
//
// HeapAlloc counts bytes allocated and not yet collected, so it includes
// garbage the collector has not got to. Plotted over time it sawtooths between
// collections, and the teeth are an artefact of when the GC happened to run —
// they say nothing about the program. /gc/heap/live:bytes is what survived the
// last mark: the memory fsforge is actually holding. That is the claim being
// made, and it is a far steadier line.
//
// HeapAlloc is kept alongside because the peak of it is what a machine has to
// have available, which is a fair question even if it is a noisy curve.
const (
	metricLive = "/gc/heap/live:bytes"
	metricHeap = "/memory/classes/heap/objects:bytes"
)

// countingDevice wraps a device and counts the bytes written through it.
//
// Counting here rather than watching the file grow is the difference between
// measuring work and measuring appearance: an image file is created at its full
// size and stays sparse, so its length is fixed from the first moment and tells
// you nothing.
type countingDevice struct {
	device.Device
	written atomic.Int64
}

func (c *countingDevice) WriteAt(p []byte, off int64) (int, error) {
	n, err := c.Device.WriteAt(p, off)
	c.written.Add(int64(n))
	return n, err
}

func (c *countingDevice) total() int64 { return c.written.Load() }

// Discard passes through when the underlying device supports it, and is
// counted as no bytes: punching a hole is the opposite of writing.
func (c *countingDevice) Discard(off, length int64) error {
	if d, ok := c.Device.(device.Discarder); ok {
		return d.Discard(off, length)
	}
	return nil
}

// sampler reads both curves on one clock until it is stopped.
type sampler struct {
	counter *countingDevice
	every   time.Duration
	start   time.Time

	done    chan struct{}
	stopped chan struct{}
	once    sync.Once

	mu       sync.Mutex
	samples  []Sample
	peak     uint64
	peakLive uint64

	metrics []metrics.Sample
}

func startSampler(counter *countingDevice, every time.Duration) *sampler {
	s := &sampler{
		counter: counter,
		every:   every,
		start:   time.Now(),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
		metrics: []metrics.Sample{{Name: metricLive}, {Name: metricHeap}},
	}
	go s.loop()
	return s
}

func (s *sampler) loop() {
	defer close(s.stopped)
	tick := time.NewTicker(s.every)
	defer tick.Stop()

	for {
		select {
		case <-s.done:
			s.record() // one last reading, so the curves end where the build did
			return
		case <-tick.C:
			s.record()
		}
	}
}

func (s *sampler) record() {
	// metrics.Read does not stop the world, unlike ReadMemStats, so sampling
	// every ten milliseconds costs the build almost nothing — the measurement
	// stays out of the way of what it is measuring.
	metrics.Read(s.metrics)
	live := s.metrics[0].Value.Uint64()
	heap := s.metrics[1].Value.Uint64()

	s.mu.Lock()
	defer s.mu.Unlock()
	if heap > s.peak {
		s.peak = heap
	}
	if live > s.peakLive {
		s.peakLive = live
	}
	s.samples = append(s.samples, Sample{
		T:       time.Since(s.start).Seconds(),
		Live:    live,
		Heap:    heap,
		Written: s.counter.total(),
	})
}

// stop ends sampling and returns what was collected: the samples, the peak of
// what was held, and the peak of what was allocated.
func (s *sampler) stop() ([]Sample, uint64, uint64) {
	s.once.Do(func() {
		close(s.done)
		<-s.stopped
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.samples, s.peakLive, s.peak
}

// HostInfo records enough of the machine for a reader to know what the numbers
// are worth. A throughput figure without a CPU count is a number without units.
type HostInfo struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	NumCPU int    `json:"numCPU"`
	// MaxProcs is what Go was actually allowed to use, which is not NumCPU when
	// a container or a taskset says otherwise — and that is the number the
	// timings depend on.
	MaxProcs int    `json:"maxProcs"`
	CPU      string `json:"cpu,omitempty"`
	Version  string `json:"goVersion"`
}

func hostInfo() HostInfo {
	return HostInfo{
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
		NumCPU:   runtime.NumCPU(),
		MaxProcs: runtime.GOMAXPROCS(0),
		CPU:      cpuModel(),
		Version:  runtime.Version(),
	}
}

// tidyCPU strips the trademark noise and the clock from a model name. The clock
// is misleading on a laptop part that spends its life somewhere else, and the
// symbols cost width the chart would rather give to the name.
func tidyCPU(s string) string {
	for _, junk := range []string{"(R)", "(TM)", "(r)", "(tm)", "CPU"} {
		s = strings.ReplaceAll(s, junk, "")
	}
	if i := strings.Index(s, " @ "); i >= 0 {
		s = s[:i]
	}
	return strings.Join(strings.Fields(s), " ")
}
