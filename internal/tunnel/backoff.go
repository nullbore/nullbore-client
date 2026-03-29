package tunnel

import (
	"math"
	"math/rand"
	"time"
)

// Backoff implements exponential backoff with jitter.
// Pattern borrowed from chisel (jpillora/backoff).
type Backoff struct {
	attempt float64
	Min     time.Duration
	Max     time.Duration
	Jitter  bool
}

func NewBackoff() *Backoff {
	return &Backoff{
		Min:    500 * time.Millisecond,
		Max:    30 * time.Second,
		Jitter: true,
	}
}

// Duration returns the next backoff duration and increments the attempt counter.
func (b *Backoff) Duration() time.Duration {
	d := b.forAttempt(b.attempt)
	b.attempt++
	return d
}

// Reset sets the attempt counter back to zero.
func (b *Backoff) Reset() {
	b.attempt = 0
}

// Attempt returns the current attempt number.
func (b *Backoff) Attempt() float64 {
	return b.attempt
}

func (b *Backoff) forAttempt(attempt float64) time.Duration {
	min := b.Min
	max := b.Max

	if min >= max {
		return max
	}

	// Calculate: min * 2^attempt
	d := min * time.Duration(math.Pow(2, attempt))

	// Cap at max
	if d > max || d <= 0 {
		d = max
	}

	// Add jitter: random value between [min, d]
	if b.Jitter {
		d = min + time.Duration(rand.Float64()*float64(d-min))
	}

	return d
}
