package gateway

import (
	"flag"
	"os"
	"runtime"
	"testing"

	"github.com/cordum/cordum/core/policysign"
)

func TestMain(m *testing.M) {
	// Windows keeps closed localhost sockets unavailable long enough that
	// the gateway package's many t.Parallel miniredis/httptest fixtures can
	// exhaust ephemeral ports before go-redis has a chance to reconnect.
	// Keep the default developer command usable on Windows while leaving
	// Linux CI/package parallelism unchanged.
	if runtime.GOOS == "windows" {
		_ = flag.Set("test.parallel", "1")
	}

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
	code := m.Run()
	closeWindowsGatewayRedis()
	os.Exit(code)
}
