package gateway

import (
	"sync"
	"time"
)

// statusCacheMaxEntries bounds the number of cached status responses.
// Entries are keyed by caller role and tenant, so in deployments where the
// tenant header is not authenticated a client could otherwise grow the map
// without limit.
const statusCacheMaxEntries = 256

// statusCache caches the /api/v1/status response to avoid repeated Redis
// PING + snapshot reads + pipeline counts on every dashboard poll.
//
// The response depends on the caller: admins receive infrastructure fields
// that other roles must not see, and the pipeline counts are per tenant. Each
// response is therefore cached under a key built by statusCacheKey, and a
// lookup only ever returns an entry written for the same key.
type statusCache struct {
	mu      sync.RWMutex
	entries map[string]statusCacheEntry
	ttl     time.Duration
}

type statusCacheEntry struct {
	data      map[string]any
	fetchedAt time.Time
}

func newStatusCache(ttl time.Duration) *statusCache {
	if ttl <= 0 {
		ttl = 2 * time.Second
	}
	return &statusCache{ttl: ttl}
}

// statusCacheKey partitions cached status responses by everything the
// response varies on: whether the caller is an admin, and the tenant.
func statusCacheKey(isAdmin bool, tenant string) string {
	if isAdmin {
		return "admin|" + tenant
	}
	return "member|" + tenant
}

// Get returns a shallow copy of the status cached under key if still fresh.
// Returns nil on miss. Callers may mutate the returned map without changing
// the cache.
func (c *statusCache) Get(key string) map[string]any {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || entry.data == nil || time.Since(entry.fetchedAt) > c.ttl {
		return nil
	}
	out := make(map[string]any, len(entry.data))
	for k, v := range entry.data {
		out[k] = v
	}
	return out
}

// Set stores a fresh status response under key. It drops expired entries
// first, and starts over when the cache is still full.
func (c *statusCache) Set(key string, data map[string]any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, entry := range c.entries {
		if now.Sub(entry.fetchedAt) > c.ttl {
			delete(c.entries, k)
		}
	}
	if _, exists := c.entries[key]; c.entries == nil || (!exists && len(c.entries) >= statusCacheMaxEntries) {
		c.entries = make(map[string]statusCacheEntry)
	}
	c.entries[key] = statusCacheEntry{data: data, fetchedAt: now}
}

// Invalidate clears every cached response, forcing the next Get to return nil.
func (c *statusCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}
