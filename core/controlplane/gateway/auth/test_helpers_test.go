package auth

import (
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestMiniredis creates a miniredis server with cleanup registered via t.Cleanup.
func newTestMiniredis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// newTestRedisClient creates a Redis client with constrained pool settings to
// avoid socket exhaustion on Windows where TIME_WAIT holds sockets for 240s.
// Under -count=3 with many test files, default pool size (10) across hundreds
// of miniredis instances exhausts ephemeral ports.
func newTestRedisClient(t *testing.T, addr string) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		PoolSize:     3,
		MinIdleConns: 0,
		MaxRetries:   3,
	})
	t.Cleanup(func() { _ = client.Close() })
	return client
}
