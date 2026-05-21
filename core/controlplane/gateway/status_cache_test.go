package gateway

import (
	"testing"
	"time"
)

func TestStatusCache_Miss(t *testing.T) {
	c := newStatusCache(time.Second)
	if got := c.Get(); got != nil {
		t.Fatalf("empty cache should return nil, got %v", got)
	}
}

func TestStatusCache_HitWithinTTL(t *testing.T) {
	c := newStatusCache(5 * time.Second)
	data := map[string]any{"workers": 3, "uptime": 100}
	c.Set(data)

	got := c.Get()
	if got == nil {
		t.Fatal("cache should return data within TTL")
	}
	if got["workers"] != 3 {
		t.Fatalf("expected workers=3, got %v", got["workers"])
	}
}

func TestStatusCache_GetReadOnlyDoesNotMutateCaller(t *testing.T) {
	c := newStatusCache(5 * time.Second)
	c.Set(map[string]any{"workers": 3, "uptime": 100})

	got := c.Get()
	if got == nil {
		t.Fatal("cache should return data within TTL")
	}
	got["workers"] = 99
	got["new_key"] = "caller-local"

	again := c.Get()
	if again["workers"] != 3 {
		t.Fatalf("caller mutation changed cached workers = %v, want 3", again["workers"])
	}
	if _, ok := again["new_key"]; ok {
		t.Fatalf("caller-added key leaked into cache: %#v", again)
	}
}

func TestStatusCache_MissAfterTTL(t *testing.T) {
	c := newStatusCache(10 * time.Millisecond)
	c.Set(map[string]any{"test": true})

	time.Sleep(20 * time.Millisecond)

	if got := c.Get(); got != nil {
		t.Fatal("cache should expire after TTL")
	}
}

func TestStatusCache_Invalidate(t *testing.T) {
	c := newStatusCache(5 * time.Second)
	c.Set(map[string]any{"test": true})

	c.Invalidate()

	if got := c.Get(); got != nil {
		t.Fatal("cache should be empty after invalidation")
	}
}

func TestStatusCache_NilSafe(t *testing.T) {
	var c *statusCache
	// All methods should be safe to call on nil
	if got := c.Get(); got != nil {
		t.Fatal("nil cache Get should return nil")
	}
	c.Set(map[string]any{"test": true}) // should not panic
	c.Invalidate()                      // should not panic
}
