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
	// Default policy-signing mode for tests: off. Signing-specific
	// tests opt in explicitly via t.Setenv(policysign.EnvStrictMode,…).
	// Without this, every bundle-save test in the gateway package
	// would hit the 503 "signing key not configured" path — which is
	// correct production behaviour, but drowns out the tests that are
	// checking something else.
	if os.Getenv(policysign.EnvStrictMode) == "" {
		_ = os.Setenv(policysign.EnvStrictMode, "off")
	}
	os.Exit(m.Run())
}
