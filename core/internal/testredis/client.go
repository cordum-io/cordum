package testredis

import (
	"net/url"
	"os"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
)

const (
	RedisPoolSizeEnv     = "REDIS_POOL_SIZE"
	RedisMinIdleConnsEnv = "REDIS_MIN_IDLE_CONNS"

	// PoolSize keeps miniredis-backed tests from opening go-redis's
	// default per-process connection fanout under broad package sweeps.
	PoolSize = 1

	// MinIdleConns avoids eager background connections to short-lived
	// in-process Redis servers used by tests.
	MinIdleConns = 0
)

// Options returns bounded Redis options for miniredis-backed tests.
func Options(addr string) *redis.Options {
	return &redis.Options{
		Addr:         addr,
		PoolSize:     PoolSize,
		MinIdleConns: MinIdleConns,
	}
}

// URL returns a redis:// URL with bounded pool query parameters for tests.
func URL(addr string) string {
	query := url.Values{}
	query.Set("pool_size", strconv.Itoa(PoolSize))
	query.Set("min_idle_conns", strconv.Itoa(MinIdleConns))
	return (&url.URL{
		Scheme:   "redis",
		Host:     addr,
		RawQuery: query.Encode(),
	}).String()
}

// NewClient creates a bounded Redis client and registers cleanup with t.
func NewClient(t testing.TB, addr string) *redis.Client {
	t.Helper()
	client := redis.NewClient(Options(addr))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// ApplyPoolEnv constrains redisutil.NewClient for a test process.
func ApplyPoolEnv() func() {
	return applyEnv(map[string]string{
		RedisPoolSizeEnv:     strconv.Itoa(PoolSize),
		RedisMinIdleConnsEnv: strconv.Itoa(MinIdleConns),
	})
}

func applyEnv(values map[string]string) func() {
	type previous struct {
		value string
		set   bool
	}
	old := make(map[string]previous, len(values))
	for name, value := range values {
		prev, ok := os.LookupEnv(name)
		old[name] = previous{value: prev, set: ok}
		_ = os.Setenv(name, value)
	}
	return func() {
		for name, prev := range old {
			if prev.set {
				_ = os.Setenv(name, prev.value)
			} else {
				_ = os.Unsetenv(name)
			}
		}
	}
}
