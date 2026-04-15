package gateway

import (
	"fmt"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisRateLimiterBasic(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()

	rl := newRedisRateLimiter(client, 10, 10)

	// First 10 requests should be allowed.
	for i := 0; i < 10; i++ {
		if !rl.Allow("test-key") {
			t.Fatalf("request %d: expected allow", i+1)
		}
	}

	// 11th request should be rejected.
	if rl.Allow("test-key") {
		t.Fatal("request 11: expected reject")
	}

	// Use a different key suffix to simulate a new time window. In production,
	// the key rotates every second because it includes the unix timestamp.
	// Here we just verify a fresh key gets a fresh quota.
	if !rl.Allow("test-key-window2") {
		t.Fatal("expected allow for new key (simulating new window)")
	}
}

func TestRedisRateLimiterMultiReplica(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	client1 := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client1.Close() }()
	client2 := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client2.Close() }()

	rl1 := newRedisRateLimiter(client1, 10, 10)
	rl2 := newRedisRateLimiter(client2, 10, 10)

	// Each replica uses 5 of the 10-burst quota.
	for i := 0; i < 5; i++ {
		if !rl1.Allow("shared-key") {
			t.Fatalf("replica1 request %d: expected allow", i+1)
		}
	}
	for i := 0; i < 5; i++ {
		if !rl2.Allow("shared-key") {
			t.Fatalf("replica2 request %d: expected allow", i+1)
		}
	}

	// Combined total = 10. Next request from either should be rejected.
	if rl1.Allow("shared-key") {
		t.Fatal("replica1 request 6: expected reject (combined burst exceeded)")
	}
	if rl2.Allow("shared-key") {
		t.Fatal("replica2 request 6: expected reject (combined burst exceeded)")
	}
}

func TestRedisRateLimiterFallback(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}

	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()

	rl := newRedisRateLimiter(client, 10, 10)

	// Close miniredis to simulate Redis failure.
	srv.Close()

	// Should fail-secure (deny) when Redis is unavailable — rate limiting is
	// a security control, so we reject rather than allow unbounded traffic.
	if rl.Allow("fallback-key") {
		t.Fatal("expected deny when Redis unavailable (fail-secure)")
	}
}

func TestRedisRateLimiterKeyFormat(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()

	rl := newRedisRateLimiter(client, 10, 10)
	rl.Allow("tenant:acme")

	// Check that a key matching the expected pattern exists.
	now := time.Now().Unix()
	expectedKey := fmt.Sprintf("cordum:rl:tenant:acme:%d", now)
	if !srv.Exists(expectedKey) {
		// Allow ±1 second for timing.
		altKey := fmt.Sprintf("cordum:rl:tenant:acme:%d", now-1)
		if !srv.Exists(altKey) {
			keys := srv.Keys()
			t.Fatalf("expected Redis key %q (or %q), got keys: %v", expectedKey, altKey, keys)
		}
	}
}

func TestRedisRateLimiterNilClient(t *testing.T) {
	rl := newRedisRateLimiter(nil, 10, 10)

	// With nil client, should use in-memory fallback.
	if !rl.Allow("test") {
		t.Fatal("expected allow with nil Redis client (in-memory fallback)")
	}
}

func TestRedisRateLimiterNilReceiver(t *testing.T) {
	var rl *redisRateLimiter
	if !rl.Allow("test") {
		t.Fatal("expected allow with nil receiver")
	}
}

// TestRedisRateLimiter_RedTeam14_BurstExceeded verifies the red-team finding #14:
// 60 rapid requests with a burst limit of 50 must trigger a rate limit rejection.
func TestRedisRateLimiter_RedTeam14_BurstExceeded(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()

	// Simulate dev .env: RPS=50, burst=100.
	rl := newRedisRateLimiter(client, 50, 100)

	allowed := 0
	rejected := 0
	for i := 0; i < 120; i++ {
		if rl.Allow("dev-tenant") {
			allowed++
		} else {
			rejected++
		}
	}

	if rejected == 0 {
		t.Fatalf("expected some requests rejected after burst, but all %d were allowed", allowed)
	}
	if allowed > 100 {
		t.Fatalf("expected at most 100 allowed (burst limit), got %d", allowed)
	}
	t.Logf("burst test: %d allowed, %d rejected (burst=100)", allowed, rejected)
}

// TestKeyedRateLimiter_BurstEnforced validates the in-memory limiter rejects after burst.
func TestKeyedRateLimiter_BurstEnforced(t *testing.T) {
	rl := newKeyedRateLimiter(5, 10)

	allowed := 0
	for i := 0; i < 20; i++ {
		if rl.Allow("key") {
			allowed++
		}
	}

	if allowed > 10 {
		t.Fatalf("expected at most 10 allowed (burst), got %d", allowed)
	}
	if allowed < 5 {
		t.Fatalf("expected at least 5 allowed (rps), got %d", allowed)
	}
}
