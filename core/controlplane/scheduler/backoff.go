package scheduler

import (
	"math/rand"
	"time"
)

const (
	backoffBase      = 1 * time.Second
	backoffMax       = 30 * time.Second
	backoffJitterMax = 500 * time.Millisecond
	backoffMaxShift  = 10 // clamp exponent to avoid overflow
)

// backoffDelay returns an exponential backoff duration with jitter.
// Formula: min(base * 2^attempt + jitter, maxDelay)
func backoffDelay(attempt int, base, maxDelay time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > backoffMaxShift {
		attempt = backoffMaxShift
	}
	delay := base << uint(attempt)
	if delay > maxDelay || delay <= 0 {
		delay = maxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(backoffJitterMax)))
	total := delay + jitter
	if total > maxDelay {
		total = maxDelay
	}
	return total
}
