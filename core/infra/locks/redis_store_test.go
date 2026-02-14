package locks

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRedisStoreAcquireRelease(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := NewRedisStore("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	lock, ok, err := store.Acquire(ctx, "repo:alpha", "worker-a", ModeExclusive, 2*time.Second)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !ok || lock == nil {
		t.Fatalf("expected lock acquired")
	}

	if _, ok, err := store.Acquire(ctx, "repo:alpha", "worker-b", ModeExclusive, 2*time.Second); err == nil && ok {
		t.Fatalf("expected second exclusive acquire to fail")
	}

	if _, ok, err := store.Release(ctx, "repo:alpha", "worker-a"); err != nil {
		t.Fatalf("release: %v", err)
	} else if !ok {
		t.Fatalf("expected release ok")
	}

	if _, ok, err := store.Acquire(ctx, "repo:alpha", "worker-b", ModeExclusive, 2*time.Second); err != nil || !ok {
		t.Fatalf("expected acquire after release, err=%v ok=%v", err, ok)
	}
}

func TestRedisStoreShared(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := NewRedisStore("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if _, ok, err := store.Acquire(ctx, "repo:shared", "worker-a", ModeShared, 2*time.Second); err != nil {
		t.Fatalf("acquire shared: %v", err)
	} else if !ok {
		t.Fatalf("expected shared acquire ok")
	}
	if _, ok, err := store.Acquire(ctx, "repo:shared", "worker-b", ModeShared, 2*time.Second); err != nil || !ok {
		t.Fatalf("expected shared acquire ok, err=%v ok=%v", err, ok)
	}
	if _, ok, err := store.Acquire(ctx, "repo:shared", "worker-c", ModeExclusive, 2*time.Second); err == nil && ok {
		t.Fatalf("expected exclusive acquire to fail while shared held")
	}
	if _, ok, err := store.Release(ctx, "repo:shared", "worker-a"); err != nil || !ok {
		t.Fatalf("expected release ok, err=%v ok=%v", err, ok)
	}
	if lock, err := store.Get(ctx, "repo:shared"); err != nil || lock == nil {
		t.Fatalf("expected lock to remain after partial release")
	}
	if _, ok, err := store.Release(ctx, "repo:shared", "worker-b"); err != nil || !ok {
		t.Fatalf("expected release ok, err=%v ok=%v", err, ok)
	}
	if _, err := store.Get(ctx, "repo:shared"); err == nil {
		t.Fatalf("expected lock to be cleared")
	}
}

func TestRedisStoreRenew(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := NewRedisStore("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if _, ok, err := store.Acquire(ctx, "repo:renew", "worker-a", ModeExclusive, 2*time.Second); err != nil {
		t.Fatalf("acquire: %v", err)
	} else if !ok {
		t.Fatalf("expected acquire ok")
	}
	if _, ok, err := store.Renew(ctx, "repo:renew", "worker-a", 3*time.Second); err != nil || !ok {
		t.Fatalf("expected renew ok, err=%v ok=%v", err, ok)
	}
}

func TestRedisStoreModeChangeRejectedMultiOwner(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := NewRedisStore("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Two owners acquire shared lock
	if _, ok, err := store.Acquire(ctx, "repo:mode", "worker-a", ModeShared, 2*time.Second); err != nil {
		t.Fatalf("acquire shared A: %v", err)
	} else if !ok {
		t.Fatalf("expected shared acquire A ok")
	}
	if _, ok, err := store.Acquire(ctx, "repo:mode", "worker-b", ModeShared, 2*time.Second); err != nil || !ok {
		t.Fatalf("expected shared acquire B ok, err=%v ok=%v", err, ok)
	}

	// Worker A tries to upgrade to exclusive — should be rejected because worker B also holds it
	if _, ok, err := store.Acquire(ctx, "repo:mode", "worker-a", ModeExclusive, 2*time.Second); err == nil && ok {
		t.Fatalf("expected exclusive upgrade to be rejected when multiple owners hold shared lock")
	}
}

func TestRedisStoreSingleOwnerUpgrade(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := NewRedisStore("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Single owner acquires shared lock
	if _, ok, err := store.Acquire(ctx, "repo:upgrade", "worker-a", ModeShared, 2*time.Second); err != nil {
		t.Fatalf("acquire shared: %v", err)
	} else if !ok {
		t.Fatalf("expected shared acquire ok")
	}

	// Same owner upgrades to exclusive — should succeed (sole owner)
	lock, ok, err := store.Acquire(ctx, "repo:upgrade", "worker-a", ModeExclusive, 2*time.Second)
	if err != nil || !ok {
		t.Fatalf("expected single-owner upgrade to exclusive, err=%v ok=%v", err, ok)
	}
	if lock == nil || lock.Mode != ModeExclusive {
		t.Fatalf("expected lock mode to be exclusive after upgrade")
	}
}

func TestRedisStoreReleasePTTLPreserved(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := NewRedisStore("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Two owners acquire shared lock
	if _, ok, err := store.Acquire(ctx, "repo:pttl", "worker-a", ModeShared, 5*time.Second); err != nil {
		t.Fatalf("acquire shared A: %v", err)
	} else if !ok {
		t.Fatalf("expected shared acquire A ok")
	}
	if _, ok, err := store.Acquire(ctx, "repo:pttl", "worker-b", ModeShared, 5*time.Second); err != nil || !ok {
		t.Fatalf("expected shared acquire B ok, err=%v ok=%v", err, ok)
	}

	// Release one owner — lock should still exist with TTL preserved
	lock, ok, err := store.Release(ctx, "repo:pttl", "worker-a")
	if err != nil || !ok {
		t.Fatalf("expected release ok, err=%v ok=%v", err, ok)
	}
	if lock == nil {
		t.Fatalf("expected lock to remain after partial release")
	}

	// Verify lock still has TTL (key should still expire, not be permanent)
	ttl := mr.TTL("lock:repo:pttl")
	if ttl == 0 {
		t.Fatalf("expected lock to have TTL after partial release, got no expiry")
	}
}
