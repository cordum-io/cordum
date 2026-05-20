package gateway

import (
	"net/url"
	"strconv"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/redis/go-redis/v9"
)

const (
	gatewayTestRedisPoolSize     = 3
	gatewayTestRedisMinIdleConns = 0
	gatewayTestRedisMaxRetries   = 3
)

// maxLoginAttempts mirrors auth.maxLoginAttempts for gateway handler tests.
func maxLoginAttempts() int {
	return intFromEnv("MAX_LOGIN_ATTEMPTS", 5)
}

// newTestUserStore creates a RedisUserStore backed by miniredis for testing.
// This helper bridges gateway tests that need a user store after the
// implementation moved to the auth/ sub-package.
func newTestUserStore(t *testing.T) (*auth.RedisUserStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	store, err := auth.NewRedisUserStore(testRedisURL(srv.Addr()))
	if err != nil {
		t.Fatalf("NewRedisUserStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return store, srv
}

func testRedisOptions(addr string) *redis.Options {
	return &redis.Options{
		Addr:         addr,
		PoolSize:     1,
		MinIdleConns: 0,
		MaxRetries:   1,
	}
}

func testRedisURL(addr string) string {
	u := url.URL{
		Scheme: "redis",
		Host:   addr,
	}
	q := u.Query()
	q.Set("pool_size", strconv.Itoa(gatewayTestRedisPoolSize))
	q.Set("min_idle_conns", strconv.Itoa(gatewayTestRedisMinIdleConns))
	q.Set("max_retries", strconv.Itoa(gatewayTestRedisMaxRetries))
	u.RawQuery = q.Encode()
	return u.String()
}
