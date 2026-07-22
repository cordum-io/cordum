package store

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
	defer func() { _ = store.Close() }()

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
	for _, key := range []string{"ctx:123", "ctx:run:step@1"} {
		ptr := PointerForKey(key)
		gotKey, err := KeyFromPointer(ptr)
		if err != nil {
			t.Fatalf("key from pointer %q: %v", ptr, err)
		}
		if gotKey != key {
			t.Fatalf("key from pointer = %q, want %q", gotKey, key)
		}
	}

	if _, err := KeyFromPointer("invalid"); err == nil {
		t.Fatalf("expected error for invalid pointer")
	}
}

func TestKeyFromPointerRejectsNonCanonicalRedisPointers(t *testing.T) {
	tests := []string{
		" redis://ctx:123",
		"redis://ctx:123 ",
		"redis://user@ctx:123",
		"redis://ctx:123?token=secret",
		"redis://ctx:123#fragment",
		"redis://ctx%3A123",
		"redis://ctx:../other",
		"redis://ctx:\x00other",
		"redis://ctx:123/other",
		"redis://ctx:123\\other",
	}
	for _, ptr := range tests {
		t.Run(ptr, func(t *testing.T) {
			if key, err := KeyFromPointer(ptr); err == nil {
				t.Fatalf("expected rejection, got key %q", key)
			}
		})
	}
}

func TestRedisStoreRespectsContextCancellation(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	if err := store.PutContext(ctx, "ctx:cancelled", []byte("data")); err == nil {
		t.Fatal("expected error from cancelled context on PutContext")
	}
	if _, err := store.GetContext(ctx, "ctx:cancelled"); err == nil {
		t.Fatal("expected error from cancelled context on GetContext")
	}
	if err := store.PutResult(ctx, "res:cancelled", []byte("data")); err == nil {
		t.Fatal("expected error from cancelled context on PutResult")
	}
	if _, err := store.GetResult(ctx, "res:cancelled"); err == nil {
		t.Fatal("expected error from cancelled context on GetResult")
	}
}
