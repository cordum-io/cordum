package gateway

import (
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

// maxLoginAttempts mirrors auth.maxLoginAttempts for gateway handler tests.
func maxLoginAttempts() int {
	return intFromEnv("MAX_LOGIN_ATTEMPTS", 5)
}

// newTestUserStore creates a RedisUserStore backed by miniredis for testing.
// This helper bridges gateway tests that need a user store after the
// implementation moved to the auth/ sub-package.
func newTestUserStore(t *testing.T) (*RedisUserStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	store, err := NewRedisUserStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("NewRedisUserStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return store, srv
}
