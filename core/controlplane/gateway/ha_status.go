package gateway

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Default circuit breaker thresholds — must match scheduler/safety_client.go values.
const (
	cbFailThreshold = 3
	cbOpenDuration  = 30 * time.Second
)

// CircuitBreakerStatus describes the observed state of a distributed circuit breaker.
type CircuitBreakerStatus struct {
	State              string `json:"state"`                // CLOSED, OPEN, or UNKNOWN
	Failures           int64  `json:"failures"`             // Current failure count (-1 if unknown)
	FailThreshold      int    `json:"fail_threshold"`       // Threshold for tripping
	CooldownRemainingMs int64 `json:"cooldown_remaining_ms"` // ms until key expires (0 if closed)
}

// readCircuitBreakerStatus reads a circuit breaker's state from Redis.
// keyPrefix should be "cordum:cb:safety" or "cordum:cb:safety:output".
// Returns a safe default if Redis is unavailable.
func readCircuitBreakerStatus(ctx context.Context, rdb redis.UniversalClient, keyPrefix string) CircuitBreakerStatus {
	failKey := keyPrefix + ":failures"
	unknown := CircuitBreakerStatus{
		State:         "UNKNOWN",
		Failures:      -1,
		FailThreshold: cbFailThreshold,
	}

	if rdb == nil {
		return unknown
	}

	opCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	pipe := rdb.Pipeline()
	getCmd := pipe.Get(opCtx, failKey)
	ttlCmd := pipe.PTTL(opCtx, failKey)
	_, _ = pipe.Exec(opCtx)

	count, err := getCmd.Int64()
	if err != nil {
		if err == redis.Nil {
			// No failures key — circuit is closed.
			return CircuitBreakerStatus{
				State:         "CLOSED",
				Failures:      0,
				FailThreshold: cbFailThreshold,
			}
		}
		return unknown
	}

	ttl, _ := ttlCmd.Result()
	cooldownMs := int64(0)
	if ttl > 0 {
		cooldownMs = ttl.Milliseconds()
	}

	state := "CLOSED"
	if count >= int64(cbFailThreshold) && ttl > 0 {
		state = "OPEN"
	}

	return CircuitBreakerStatus{
		State:              state,
		Failures:           count,
		FailThreshold:      cbFailThreshold,
		CooldownRemainingMs: cooldownMs,
	}
}

// rateLimiterMode returns "redis" or "memory" based on the rate limiter type.
func rateLimiterMode(rl rateLimiter) string {
	if _, ok := rl.(*redisRateLimiter); ok {
		return "redis"
	}
	return "memory"
}
