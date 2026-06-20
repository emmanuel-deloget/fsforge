package image

import (
	"crypto/rand"
	"time"
)

// Clock is the only source of "now" an engine may use. Injecting it removes
// time.Now() from the engines and is what makes timestamps reproducible.
type Clock interface {
	Now() time.Time
}

// UUIDSource is the only source of randomness for filesystem identifiers.
type UUIDSource interface {
	UUID() [16]byte
}

// SystemClock returns the host wall clock. Use for non-reproducible builds.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// FixedClock always returns T. Use for reproducible builds (e.g. fed from
// SOURCE_DATE_EPOCH).
type FixedClock struct{ T time.Time }

func (c FixedClock) Now() time.Time { return c.T }

// RandomUUID produces RFC 4122 v4 UUIDs from the crypto RNG.
type RandomUUID struct{}

func (RandomUUID) UUID() [16]byte {
	var u [16]byte
	_, _ = rand.Read(u[:])
	u[6] = (u[6] & 0x0f) | 0x40 // version 4
	u[8] = (u[8] & 0x3f) | 0x80 // variant 1
	return u
}

// FixedUUID always returns V. Use for reproducible builds.
type FixedUUID struct{ V [16]byte }

func (f FixedUUID) UUID() [16]byte { return f.V }
