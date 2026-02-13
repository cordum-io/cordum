package scheduler

import (
	"crypto/rand"
	"math/big"
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
	delay := base << attempt
	if delay > maxDelay || delay <= 0 {
		delay = maxDelay
	}
	jitter := cryptoJitter(backoffJitterMax)
	total := delay + jitter
	if total > maxDelay {
		total = maxDelay
	}
	return total
}

func cryptoJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}
