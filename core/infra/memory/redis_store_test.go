package memory

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRedisStoreContextAndResult(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	ctxKey := MakeContextKey("job-1")
	resKey := MakeResultKey("job-1")

	if err := store.PutContext(ctx, ctxKey, []byte(`{"prompt":"hello"}`)); err != nil {
		t.Fatalf("put context: %v", err)
	}
	if err := store.PutResult(ctx, resKey, []byte(`{"result":"ok"}`)); err != nil {
		t.Fatalf("put result: %v", err)
	}

	if ttl := srv.TTL(ctxKey); ttl <= 0 || ttl > defaultDataTTL {
		t.Fatalf("context TTL not set correctly, got %v", ttl)
	}
	if ttl := srv.TTL(resKey); ttl <= 0 || ttl > defaultDataTTL {
		t.Fatalf("result TTL not set correctly, got %v", ttl)
	}

	gotCtx, err := store.GetContext(ctx, ctxKey)
	if err != nil {
		t.Fatalf("get context: %v", err)
	}
	if string(gotCtx) != `{"prompt":"hello"}` {
		t.Fatalf("unexpected context payload: %s", string(gotCtx))
	}

	gotRes, err := store.GetResult(ctx, resKey)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	if string(gotRes) != `{"result":"ok"}` {
		t.Fatalf("unexpected result payload: %s", string(gotRes))
	}
}

func TestKeyPointerHelpers(t *testing.T) {
	key := "ctx:123"
	ptr := PointerForKey(key)
	if ptr != "redis://ctx:123" {
		t.Fatalf("unexpected pointer: %s", ptr)
	}

	gotKey, err := KeyFromPointer(ptr)
	if err != nil {
		t.Fatalf("key from pointer: %v", err)
	}
	if gotKey != key {
		t.Fatalf("unexpected key: %s", gotKey)
	}

	if _, err := KeyFromPointer("invalid"); err == nil {
		t.Fatalf("expected error for invalid pointer")
	}
}
