package agentd

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestRetentionProofDIE03 is the docs/contract/deletion-proofs.md proof for
// the agentd safe-allow cache. It is process memory, so the search is the
// cache's own storage (records and order), not Redis. Exports: none; the cache
// never leaves the agentd process.
func TestRetentionProofDIE03(t *testing.T) {
	clock := &cacheTestClock{now: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)}
	// defaultSafeAllowCacheTTL is the default of CORDUM_AGENTD_SAFE_ALLOW_CACHE_TTL.
	cache := NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: defaultSafeAllowCacheTTL, MaxEntries: 2}, clock)

	t.Run("expired entry is removed on Get", func(t *testing.T) {
		const marker = "pr2-08-die03-expired"
		putDIE03(t, cache, marker)
		requireCacheTrace(t, cache, marker, true)
		clock.Advance(defaultSafeAllowCacheTTL)
		if got, ok := cache.Get(dIE03Request(marker)); ok {
			t.Fatalf("Get after TTL hit %#v; want miss", got)
		}
		requireCacheTrace(t, cache, marker, false)
	})

	t.Run("overflow Put evicts the oldest entry", func(t *testing.T) {
		const oldest = "pr2-08-die03-oldest"
		putDIE03(t, cache, oldest)
		putDIE03(t, cache, "pr2-08-die03-second")
		requireCacheTrace(t, cache, oldest, true)
		putDIE03(t, cache, "pr2-08-die03-third")
		requireCacheTrace(t, cache, oldest, false)
		requireCacheTrace(t, cache, "pr2-08-die03-second", true)
	})
}

func dIE03Request(marker string) SafeAllowCacheRequest {
	return mutateSafeAllowCacheRequest(safeAllowCacheTestRequest(), func(req *SafeAllowCacheRequest) {
		req.ActionHash = "sha256:" + marker
	})
}

func putDIE03(t *testing.T, cache *SafeAllowCache, marker string) {
	t.Helper()
	resp := safeAllowCacheTestResponse()
	resp.ActionHash = "sha256:" + marker
	resp.Reason = marker
	if !cache.Put(dIE03Request(marker), resp) {
		t.Fatalf("Put(%s) refused", marker)
	}
}

// requireCacheTrace searches every record (key and response) and the order
// slice for marker.
func requireCacheTrace(t *testing.T, cache *SafeAllowCache, marker string, want bool) {
	t.Helper()
	cache.mu.Lock()
	storage := fmt.Sprintf("%#v", cache.records)
	found := strings.Contains(storage, marker) ||
		slices.ContainsFunc(cache.order, func(key string) bool { return strings.Contains(key, marker) })
	cache.mu.Unlock()
	if found != want {
		t.Fatalf("cache storage holds %q = %v, want %v; storage=%s order=%q", marker, found, want, storage, cache.order)
	}
}
