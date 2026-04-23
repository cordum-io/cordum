package gateway

import (
	"os"
	"testing"

	"github.com/cordum/cordum/core/policysign"
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
	// Default policy-signing strictness to off for the gateway test
	// suite. Signing-specific tests explicitly opt-in via t.Setenv so
	// they are not affected by this default. Without it, every putBundle
	// test would need to stand up a signing key.
	if os.Getenv("CORDUM_POLICY_STRICT") == "" {
		_ = os.Setenv("CORDUM_POLICY_STRICT", "off")
	}
	os.Exit(m.Run())
}
