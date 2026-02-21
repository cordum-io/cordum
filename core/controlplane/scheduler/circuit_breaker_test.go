package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() {
		rdb.Close()
		srv.Close()
	})
	return srv, rdb
}

func TestRedisCircuitBreaker_FailureThreshold(t *testing.T) {
	srv, rdb := newTestRedis(t)
	_ = srv

	cb := NewRedisCircuitBreaker(rdb, "cordum:cb:test", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  30 * time.Second,
	})
	ctx := context.Background()

	// Circuit should be closed initially.
	if cb.IsOpen(ctx) {
		t.Fatal("expected circuit to be closed initially")
	}

	// Record 2 failures — should still be closed.
	cb.RecordFailure(ctx)
	cb.RecordFailure(ctx)
	if cb.IsOpen(ctx) {
		t.Fatal("expected circuit to be closed after 2 failures")
	}

	// 3rd failure should open the circuit.
	cb.RecordFailure(ctx)
	if !cb.IsOpen(ctx) {
		t.Fatal("expected circuit to be open after 3 failures")
	}

	// Verify failure count.
	count := cb.FailureCount(ctx)
	if count != 3 {
		t.Fatalf("expected failure count 3, got %d", count)
	}
}

func TestRedisCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	srv, rdb := newTestRedis(t)

	cb := NewRedisCircuitBreaker(rdb, "cordum:cb:test", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  2 * time.Second,
	})
	ctx := context.Background()

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.RecordFailure(ctx)
	}
	if !cb.IsOpen(ctx) {
		t.Fatal("expected circuit to be open")
	}

	// Fast-forward past open duration — TTL expires, counter gone.
	srv.FastForward(3 * time.Second)

	// Circuit should now be closed (half-open: failure key expired, next request is a probe).
	if cb.IsOpen(ctx) {
		t.Fatal("expected circuit to be closed after TTL expiry (half-open probe)")
	}
}

func TestRedisCircuitBreaker_SuccessResetsCounter(t *testing.T) {
	_, rdb := newTestRedis(t)

	cb := NewRedisCircuitBreaker(rdb, "cordum:cb:test", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  30 * time.Second,
	})
	ctx := context.Background()

	// Record 2 failures.
	cb.RecordFailure(ctx)
	cb.RecordFailure(ctx)
	if cb.FailureCount(ctx) != 2 {
		t.Fatalf("expected 2 failures, got %d", cb.FailureCount(ctx))
	}

	// Success resets the counter.
	cb.RecordSuccess(ctx)
	if cb.FailureCount(ctx) != 0 {
		t.Fatalf("expected 0 failures after success, got %d", cb.FailureCount(ctx))
	}

	// Circuit should remain closed.
	if cb.IsOpen(ctx) {
		t.Fatal("expected circuit to be closed after success reset")
	}
}

func TestRedisCircuitBreaker_HalfOpenFailure(t *testing.T) {
	srv, rdb := newTestRedis(t)

	cb := NewRedisCircuitBreaker(rdb, "cordum:cb:test", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  2 * time.Second,
	})
	ctx := context.Background()

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.RecordFailure(ctx)
	}

	// Fast-forward past open duration.
	srv.FastForward(3 * time.Second)

	// Circuit should be in half-open (key expired).
	if cb.IsOpen(ctx) {
		t.Fatal("expected circuit closed after TTL expiry")
	}

	// Probe fails — circuit should re-open.
	for i := 0; i < 3; i++ {
		cb.RecordFailure(ctx)
	}
	if !cb.IsOpen(ctx) {
		t.Fatal("expected circuit to re-open after probe failure")
	}
}

func TestRedisCircuitBreaker_SharedState(t *testing.T) {
	_, rdb := newTestRedis(t)

	// Two breaker instances sharing the same Redis.
	cbA := NewRedisCircuitBreaker(rdb, "cordum:cb:shared", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  30 * time.Second,
	})
	cbB := NewRedisCircuitBreaker(rdb, "cordum:cb:shared", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  30 * time.Second,
	})
	ctx := context.Background()

	// Instance A records 2 failures.
	cbA.RecordFailure(ctx)
	cbA.RecordFailure(ctx)

	// Instance B records 1 failure — should trip the shared circuit.
	cbB.RecordFailure(ctx)

	// Both instances should see the open circuit.
	if !cbA.IsOpen(ctx) {
		t.Fatal("expected instance A to see open circuit")
	}
	if !cbB.IsOpen(ctx) {
		t.Fatal("expected instance B to see open circuit")
	}

	// Shared failure count.
	if cbA.FailureCount(ctx) != 3 {
		t.Fatalf("expected shared failure count 3, got %d", cbA.FailureCount(ctx))
	}
}

func TestRedisCircuitBreaker_RedisFallback(t *testing.T) {
	srv, rdb := newTestRedis(t)

	cb := NewRedisCircuitBreaker(rdb, "cordum:cb:test", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  30 * time.Second,
	})
	ctx := context.Background()

	// Stop Redis.
	srv.Close()

	// With Redis down, circuit should use local fallback.
	// Local breaker starts closed — should allow requests.
	if cb.IsOpen(ctx) {
		t.Fatal("expected local fallback to allow requests")
	}

	// Record failures via local fallback.
	cb.RecordFailure(ctx)
	cb.RecordFailure(ctx)
	cb.RecordFailure(ctx)

	// Local fallback should trip.
	if !cb.IsOpen(ctx) {
		t.Fatal("expected local fallback circuit to open after 3 failures")
	}
}

func TestRedisCircuitBreaker_NilRedis(t *testing.T) {
	cb := NewRedisCircuitBreaker(nil, "cordum:cb:test", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  30 * time.Second,
	})
	ctx := context.Background()

	// Should use purely local breaker — no panics.
	if cb.IsOpen(ctx) {
		t.Fatal("expected closed with nil Redis")
	}
	cb.RecordFailure(ctx)
	cb.RecordFailure(ctx)
	cb.RecordFailure(ctx)
	if !cb.IsOpen(ctx) {
		t.Fatal("expected local breaker to open after 3 failures")
	}
	cb.RecordSuccess(ctx)
}

func TestRedisCircuitBreaker_ConcurrentAccess(t *testing.T) {
	_, rdb := newTestRedis(t)

	cb := NewRedisCircuitBreaker(rdb, "cordum:cb:concurrent", CircuitBreakerOpts{
		FailThreshold: 10,
		OpenDuration:  30 * time.Second,
	})
	ctx := context.Background()

	// 5 goroutines each recording 2 failures = 10 total → should trip.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.RecordFailure(ctx)
			cb.RecordFailure(ctx)
		}()
	}
	wg.Wait()

	// Circuit should be open.
	if !cb.IsOpen(ctx) {
		t.Fatal("expected circuit to be open after concurrent failures")
	}

	count := cb.FailureCount(ctx)
	if count < 10 {
		t.Fatalf("expected at least 10 failures, got %d", count)
	}
}

func TestRedisCircuitBreaker_AllowRequest(t *testing.T) {
	_, rdb := newTestRedis(t)

	cb := NewRedisCircuitBreaker(rdb, "cordum:cb:allow", CircuitBreakerOpts{
		FailThreshold: 2,
		OpenDuration:  30 * time.Second,
	})
	ctx := context.Background()

	if !cb.AllowRequest(ctx) {
		t.Fatal("expected AllowRequest to return true when closed")
	}

	cb.RecordFailure(ctx)
	cb.RecordFailure(ctx)

	if cb.AllowRequest(ctx) {
		t.Fatal("expected AllowRequest to return false when open")
	}
}

func TestRedisCircuitBreaker_DifferentPrefixes(t *testing.T) {
	_, rdb := newTestRedis(t)

	cbInput := NewRedisCircuitBreaker(rdb, "cordum:cb:safety", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  30 * time.Second,
	})
	cbOutput := NewRedisCircuitBreaker(rdb, "cordum:cb:safety:output", CircuitBreakerOpts{
		FailThreshold: 3,
		OpenDuration:  30 * time.Second,
	})
	ctx := context.Background()

	// Trip the input circuit.
	for i := 0; i < 3; i++ {
		cbInput.RecordFailure(ctx)
	}

	// Input should be open, output should still be closed.
	if !cbInput.IsOpen(ctx) {
		t.Fatal("expected input circuit to be open")
	}
	if cbOutput.IsOpen(ctx) {
		t.Fatal("expected output circuit to be closed (different prefix)")
	}
}
