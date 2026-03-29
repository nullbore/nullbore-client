package tunnel

import (
	"testing"
	"time"
)

func TestBackoffExponential(t *testing.T) {
	b := &Backoff{
		Min:    100 * time.Millisecond,
		Max:    10 * time.Second,
		Jitter: false, // disable jitter for predictable tests
	}

	expected := []time.Duration{
		100 * time.Millisecond,  // 100ms * 2^0
		200 * time.Millisecond,  // 100ms * 2^1
		400 * time.Millisecond,  // 100ms * 2^2
		800 * time.Millisecond,  // 100ms * 2^3
		1600 * time.Millisecond, // 100ms * 2^4
	}

	for i, exp := range expected {
		d := b.Duration()
		if d != exp {
			t.Fatalf("attempt %d: expected %s, got %s", i, exp, d)
		}
	}
}

func TestBackoffMax(t *testing.T) {
	b := &Backoff{
		Min:    1 * time.Second,
		Max:    5 * time.Second,
		Jitter: false,
	}

	// Run many attempts — should cap at Max
	for i := 0; i < 20; i++ {
		d := b.Duration()
		if d > 5*time.Second {
			t.Fatalf("attempt %d: %s exceeds max 5s", i, d)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	b := &Backoff{
		Min:    100 * time.Millisecond,
		Max:    10 * time.Second,
		Jitter: false,
	}

	b.Duration() // 100ms
	b.Duration() // 200ms
	b.Duration() // 400ms

	b.Reset()

	d := b.Duration()
	if d != 100*time.Millisecond {
		t.Fatalf("after reset: expected 100ms, got %s", d)
	}
}

func TestBackoffJitter(t *testing.T) {
	b := NewBackoff() // jitter enabled by default

	// With jitter, values should vary. Run several and check they're in range.
	for i := 0; i < 10; i++ {
		d := b.Duration()
		if d < b.Min || d > b.Max {
			t.Fatalf("attempt %d: %s out of range [%s, %s]", i, d, b.Min, b.Max)
		}
	}
}
