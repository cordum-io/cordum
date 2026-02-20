package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCircuitBreaker provides a distributed circuit breaker backed by Redis.
// Multiple replicas share the same failure counter so the circuit trips globally.
// If Redis is unavailable, it falls back to a local in-memory breaker.
type RedisCircuitBreaker struct {
	rdb           redis.UniversalClient
	failuresKey   string
	openDuration  time.Duration
	failThreshold int64
	closeAfter    int

	// Local fallback state (used when Redis is unavailable).
	mu              sync.Mutex
	localState      circuitState
	localFailures   int
	localSuccesses  int
	localOpenUntil  time.Time
	localHalfOpen   int
	halfOpenMax     int
}

// CircuitBreakerOpts configures the circuit breaker thresholds.
type CircuitBreakerOpts struct {
	FailThreshold int
	OpenDuration  time.Duration
	HalfOpenMax   int
	CloseAfter    int
}

// NewRedisCircuitBreaker creates a distributed circuit breaker.
// keyPrefix should be like "cordum:cb:safety" — ":failures" is appended.
func NewRedisCircuitBreaker(rdb redis.UniversalClient, keyPrefix string, opts CircuitBreakerOpts) *RedisCircuitBreaker {
	if opts.FailThreshold <= 0 {
		opts.FailThreshold = 3
	}
	if opts.OpenDuration <= 0 {
		opts.OpenDuration = 30 * time.Second
	}
	if opts.HalfOpenMax <= 0 {
		opts.HalfOpenMax = 3
	}
	if opts.CloseAfter <= 0 {
		opts.CloseAfter = 2
	}
	return &RedisCircuitBreaker{
		rdb:           rdb,
		failuresKey:   keyPrefix + ":failures",
		openDuration:  opts.OpenDuration,
		failThreshold: int64(opts.FailThreshold),
		closeAfter:    opts.CloseAfter,
		halfOpenMax:   opts.HalfOpenMax,
	}
}

const cbOpTimeout = 2 * time.Second

// recordFailureLua atomically increments the failure counter and sets TTL if it's a new key.
// Returns the new failure count.
var recordFailureLua = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
	redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return count
`)

// IsOpen returns true if the circuit is open (too many shared failures).
// Falls back to local state if Redis is unavailable.
func (cb *RedisCircuitBreaker) IsOpen(ctx context.Context) bool {
	if cb.rdb == nil {
		return cb.localIsOpen()
	}
	opCtx, cancel := context.WithTimeout(ctx, cbOpTimeout)
	defer cancel()
	count, err := cb.rdb.Get(opCtx, cb.failuresKey).Int64()
	if err != nil {
		if err == redis.Nil {
			return false // No failures recorded — circuit closed.
		}
		slog.Debug("circuit-breaker: redis check failed, using local fallback", "key", cb.failuresKey, "error", err)
		return cb.localIsOpen()
	}
	return count >= cb.failThreshold
}

// AllowRequest checks if a request should be allowed through the circuit.
// When the circuit is open, requests are blocked. When transitioning from
// open to half-open (failure key expired), requests are allowed.
func (cb *RedisCircuitBreaker) AllowRequest(ctx context.Context) bool {
	return !cb.IsOpen(ctx)
}

// RecordFailure increments the shared failure counter in Redis with a TTL.
// The TTL equals the open duration — when it expires, the circuit naturally
// transitions to half-open (counter gone, next request is a probe).
func (cb *RedisCircuitBreaker) RecordFailure(ctx context.Context) {
	cb.localRecordFailure()
	if cb.rdb == nil {
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, cbOpTimeout)
	defer cancel()
	ttlSec := int64(cb.openDuration.Seconds())
	if ttlSec <= 0 {
		ttlSec = 30
	}
	count, err := recordFailureLua.Run(opCtx, cb.rdb, []string{cb.failuresKey}, ttlSec).Int64()
	if err != nil {
		slog.Debug("circuit-breaker: redis record failure failed", "key", cb.failuresKey, "error", err)
		return
	}
	if count >= cb.failThreshold {
		slog.Warn("circuit-breaker: circuit opened", "key", cb.failuresKey, "failures", count, "threshold", cb.failThreshold)
	}
}

// RecordSuccess resets the shared failure counter — circuit closes.
func (cb *RedisCircuitBreaker) RecordSuccess(ctx context.Context) {
	cb.localRecordSuccess()
	if cb.rdb == nil {
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, cbOpTimeout)
	defer cancel()
	if err := cb.rdb.Del(opCtx, cb.failuresKey).Err(); err != nil {
		slog.Debug("circuit-breaker: redis record success failed", "key", cb.failuresKey, "error", err)
	}
}

// FailureCount returns the current shared failure count (for observability).
func (cb *RedisCircuitBreaker) FailureCount(ctx context.Context) int64 {
	if cb.rdb == nil {
		cb.mu.Lock()
		defer cb.mu.Unlock()
		return int64(cb.localFailures)
	}
	opCtx, cancel := context.WithTimeout(ctx, cbOpTimeout)
	defer cancel()
	count, err := cb.rdb.Get(opCtx, cb.failuresKey).Int64()
	if err != nil {
		return 0
	}
	return count
}

// ---------------------------------------------------------------------------
// Local fallback (mirrors the existing in-memory circuit breaker logic)
// ---------------------------------------------------------------------------

func (cb *RedisCircuitBreaker) localIsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := time.Now()
	if cb.localState == circuitOpen && cb.localOpenUntil.Before(now) {
		cb.localState = circuitHalfOpen
		cb.localSuccesses = 0
		cb.localHalfOpen = cb.halfOpenMax
	}
	return cb.localState == circuitOpen
}

func (cb *RedisCircuitBreaker) localRecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.localState {
	case circuitClosed:
		cb.localFailures++
		if int64(cb.localFailures) >= cb.failThreshold {
			cb.localState = circuitOpen
			cb.localOpenUntil = time.Now().Add(cb.openDuration)
			cb.localFailures = 0
		}
	case circuitHalfOpen:
		cb.localState = circuitOpen
		cb.localOpenUntil = time.Now().Add(cb.openDuration)
		cb.localFailures = 0
	}
}

func (cb *RedisCircuitBreaker) localRecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.localState {
	case circuitClosed:
		cb.localFailures = 0
	case circuitHalfOpen:
		cb.localSuccesses++
		if cb.localSuccesses >= cb.closeAfter {
			cb.localState = circuitClosed
			cb.localFailures = 0
			cb.localSuccesses = 0
			cb.localHalfOpen = 0
		}
	default:
		cb.localFailures = 0
	}
}
