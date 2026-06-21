package image

import (
	"testing"
	"time"
)

func TestFixedClock(t *testing.T) {
	want := time.Unix(1000, 0).UTC()
	if got := (FixedClock{T: want}).Now(); !got.Equal(want) {
		t.Fatalf("FixedClock.Now = %v, want %v", got, want)
	}
}

func TestSystemClockMonotonicish(t *testing.T) {
	before := time.Now()
	got := SystemClock{}.Now()
	if got.Before(before.Add(-time.Second)) {
		t.Fatalf("SystemClock.Now = %v, implausibly before %v", got, before)
	}
}

func TestFixedUUID(t *testing.T) {
	want := [16]byte{1, 2, 3, 4}
	if got := (FixedUUID{V: want}).UUID(); got != want {
		t.Fatalf("FixedUUID = %v, want %v", got, want)
	}
}

func TestRandomUUIDVersionVariant(t *testing.T) {
	u := RandomUUID{}.UUID()
	if v := u[6] >> 4; v != 4 {
		t.Fatalf("UUID version nibble = %d, want 4", v)
	}
	if variant := u[8] >> 6; variant != 0b10 {
		t.Fatalf("UUID variant bits = %b, want 10", variant)
	}
	// Two draws should differ with overwhelming probability.
	if (RandomUUID{}).UUID() == u {
		t.Fatal("two RandomUUIDs were identical")
	}
}
