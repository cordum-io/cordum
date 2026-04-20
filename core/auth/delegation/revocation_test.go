package delegation

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/redisutil"
)

func newTestRevocationStore(t *testing.T) (*RedisRevocationStore, *miniredis.Miniredis) {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	t.Cleanup(srv.Close)

	client, err := redisutil.NewClient("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("redisutil.NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return NewRedisRevocationStoreFromClient(client), srv
}

func TestRedisRevocationStoreRoundTrip(t *testing.T) {
	store, srv := newTestRevocationStore(t)

	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	if err := store.Revoke(context.Background(), "jti-1", expiresAt); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	revoked, err := store.IsRevoked(context.Background(), "jti-1")
	if err != nil {
		t.Fatalf("IsRevoked() error = %v", err)
	}
	if !revoked {
		t.Fatal("expected jti to be revoked")
	}
	if ttl := srv.TTL(delegationRevocationKey("jti-1")); ttl <= 0 {
		t.Fatalf("expected redis TTL to be set, got %v", ttl)
	}
}

func TestRedisRevocationStoreNilReceiverIsSafe(t *testing.T) {
	var store *RedisRevocationStore
	revoked, err := store.IsRevoked(context.Background(), "jti-1")
	if err != nil {
		t.Fatalf("IsRevoked() error = %v", err)
	}
	if revoked {
		t.Fatal("nil store should report not revoked")
	}
}
