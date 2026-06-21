package fsforge

import (
	"os"
	"strconv"
	"time"

	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// HostDeps returns dependencies for a non-reproducible, host build: the system
// wall clock and random UUIDs. Timestamps and the filesystem UUID will differ
// between runs.
func HostDeps() image.Deps {
	return image.Deps{Clock: image.SystemClock{}, UUID: image.RandomUUID{}}
}

// ReproducibleDeps returns dependencies for a deterministic build: a clock
// fixed at the given Unix epoch (UTC) and an all-zero UUID. Identical inputs
// then produce byte-identical output. Pass SourceDateEpoch() to honour the
// SOURCE_DATE_EPOCH convention.
func ReproducibleDeps(epoch int64) image.Deps {
	return image.Deps{
		Clock: image.FixedClock{T: time.Unix(epoch, 0).UTC()},
		UUID:  image.FixedUUID{},
	}
}

// SourceDateEpoch reads the SOURCE_DATE_EPOCH environment variable, returning 0
// when it is unset or malformed. It is the conventional input to a reproducible
// build's clock.
func SourceDateEpoch() int64 {
	if v := os.Getenv("SOURCE_DATE_EPOCH"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}
