package locks

import (
	"context"
	"strings"
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
		if skipEval(err) {
			t.Skip("miniredis does not support EVAL")
		}
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
		if skipEval(err) {
			t.Skip("miniredis does not support EVAL")
		}
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
		if skipEval(err) {
			t.Skip("miniredis does not support EVAL")
		}
		t.Fatalf("acquire: %v", err)
	} else if !ok {
		t.Fatalf("expected acquire ok")
	}
	if _, ok, err := store.Renew(ctx, "repo:renew", "worker-a", 3*time.Second); err != nil || !ok {
		t.Fatalf("expected renew ok, err=%v ok=%v", err, ok)
	}
}

func skipEval(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "eval") && strings.Contains(msg, "unknown")
}
