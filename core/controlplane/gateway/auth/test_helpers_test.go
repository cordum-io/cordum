package auth

import (
	"net/url"
	"strconv"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const (
	testRedisPoolSize     = 3
	testRedisMinIdleConns = 0
	testRedisMaxRetries   = 3
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
		PoolSize:     testRedisPoolSize,
		MinIdleConns: testRedisMinIdleConns,
		MaxRetries:   testRedisMaxRetries,
	})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func testRedisURL(addr string) string {
	u := url.URL{
		Scheme: "redis",
		Host:   addr,
	}
	q := u.Query()
	q.Set("pool_size", strconv.Itoa(testRedisPoolSize))
	q.Set("min_idle_conns", strconv.Itoa(testRedisMinIdleConns))
	q.Set("max_retries", strconv.Itoa(testRedisMaxRetries))
	u.RawQuery = q.Encode()
	return u.String()
}
