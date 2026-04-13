package gateway

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Reduce Redis connection-pool sizes for test runs to prevent
	// ephemeral-port exhaustion on Windows when many tests create
	// miniredis-backed stores concurrently.
	if os.Getenv("REDIS_POOL_SIZE") == "" {
		_ = os.Setenv("REDIS_POOL_SIZE", "1")
	}
	if os.Getenv("REDIS_MIN_IDLE_CONNS") == "" {
		_ = os.Setenv("REDIS_MIN_IDLE_CONNS", "0")
	}
	os.Exit(m.Run())
}
