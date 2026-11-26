package gateway

import (
	"fmt"
	"testing"
	"time"
)

const testStatusKey = "admin|default"

func TestStatusCache_Miss(t *testing.T) {
	c := newStatusCache(time.Second)
	if got := c.Get(testStatusKey); got != nil {
		t.Fatalf("empty cache should return nil, got %v", got)
	}
}

func TestStatusCache_HitWithinTTL(t *testing.T) {
	c := newStatusCache(5 * time.Second)
	data := map[string]any{"workers": 3, "uptime": 100}
	c.Set(testStatusKey, data)

	got := c.Get(testStatusKey)
	if got == nil {
		t.Fatal("cache should return data within TTL")
	}
	if got["workers"] != 3 {
		t.Fatalf("expected workers=3, got %v", got["workers"])
	}
}

func TestStatusCache_GetReadOnlyDoesNotMutateCaller(t *testing.T) {
	c := newStatusCache(5 * time.Second)
	c.Set(testStatusKey, map[string]any{"workers": 3, "uptime": 100})

	got := c.Get(testStatusKey)
	if got == nil {
		t.Fatal("cache should return data within TTL")
	}
	got["workers"] = 99
	got["new_key"] = "caller-local"

	again := c.Get(testStatusKey)
	if again["workers"] != 3 {
		t.Fatalf("caller mutation changed cached workers = %v, want 3", again["workers"])
	}
	if _, ok := again["new_key"]; ok {
		t.Fatalf("caller-added key leaked into cache: %#v", again)
	}
}

func TestStatusCache_MissAfterTTL(t *testing.T) {
	c := newStatusCache(10 * time.Millisecond)
	c.Set(testStatusKey, map[string]any{"test": true})

	time.Sleep(20 * time.Millisecond)

	if got := c.Get(testStatusKey); got != nil {
		t.Fatal("cache should expire after TTL")
	}
}

func TestStatusCache_Invalidate(t *testing.T) {
	c := newStatusCache(5 * time.Second)
	c.Set(testStatusKey, map[string]any{"test": true})

	c.Invalidate()

	if got := c.Get(testStatusKey); got != nil {
		t.Fatal("cache should be empty after invalidation")
	}
}

func TestStatusCache_NilSafe(t *testing.T) {
	var c *statusCache
	// All methods should be safe to call on nil
	if got := c.Get(testStatusKey); got != nil {
		t.Fatal("nil cache Get should return nil")
	}
	c.Set(testStatusKey, map[string]any{"test": true}) // should not panic
	c.Invalidate()                                     // should not panic
}

func TestStatusCacheKey_PartitionsRoleAndTenant(t *testing.T) {
	if statusCacheKey(true, "t1") == statusCacheKey(false, "t1") {
		t.Fatal("admin and non-admin callers of one tenant share a cache key")
	}
	if statusCacheKey(true, "t1") == statusCacheKey(true, "t2") {
		t.Fatal("two tenants share a cache key")
	}
}

func TestStatusCache_KeysAreIsolated(t *testing.T) {
	c := newStatusCache(5 * time.Second)
	c.Set(statusCacheKey(true, "t1"), map[string]any{"instance_id": "gw-1"})

	if got := c.Get(statusCacheKey(false, "t1")); got != nil {
		t.Fatalf("non-admin key served the admin entry: %#v", got)
	}
	if got := c.Get(statusCacheKey(true, "t2")); got != nil {
		t.Fatalf("another tenant's key served the entry: %#v", got)
	}
	if got := c.Get(statusCacheKey(true, "t1")); got == nil || got["instance_id"] != "gw-1" {
		t.Fatalf("own key lost its entry: %#v", got)
	}
}

func TestStatusCache_InvalidateClearsEveryKey(t *testing.T) {
	c := newStatusCache(5 * time.Second)
	c.Set(statusCacheKey(true, "t1"), map[string]any{"a": 1})
	c.Set(statusCacheKey(false, "t2"), map[string]any{"b": 2})

	c.Invalidate()

	if c.Get(statusCacheKey(true, "t1")) != nil || c.Get(statusCacheKey(false, "t2")) != nil {
		t.Fatal("an entry survived invalidation")
	}
}

func TestStatusCache_EntriesAreBounded(t *testing.T) {
	c := newStatusCache(time.Minute)
	for i := 0; i < 3*statusCacheMaxEntries; i++ {
		c.Set(statusCacheKey(false, fmt.Sprintf("tenant-%d", i)), map[string]any{"i": i})
	}
	if n := len(c.entries); n > statusCacheMaxEntries {
		t.Fatalf("cache holds %d entries, cap is %d", n, statusCacheMaxEntries)
	}
	last := statusCacheKey(false, fmt.Sprintf("tenant-%d", 3*statusCacheMaxEntries-1))
	if got := c.Get(last); got == nil {
		t.Fatal("the most recent entry was not kept")
	}
}

func TestStatusCache_SetPrunesExpiredEntries(t *testing.T) {
	c := newStatusCache(10 * time.Millisecond)
	c.Set(statusCacheKey(false, "old"), map[string]any{"old": true})
	time.Sleep(20 * time.Millisecond)
	c.Set(statusCacheKey(false, "new"), map[string]any{"new": true})
	if _, ok := c.entries[statusCacheKey(false, "old")]; ok {
		t.Fatal("expired entry was not pruned on Set")
	}
}
