package gateway

import (
	"sync"
	"time"
)

// statusCache caches the /api/v1/status response to avoid repeated Redis
// PING + snapshot reads + pipeline counts on every dashboard poll.
type statusCache struct {
	mu      sync.RWMutex
	entries map[statusCacheKey]statusCacheEntry
	ttl     time.Duration
}

type statusCacheKey struct {
	platform bool
	admin    bool
	tenant   string
}

type statusCacheEntry struct {
	data      map[string]any
	fetchedAt time.Time
}

func newStatusCache(ttl time.Duration) *statusCache {
	if ttl <= 0 {
		ttl = 2 * time.Second
	}
	return &statusCache{ttl: ttl, entries: make(map[statusCacheKey]statusCacheEntry)}
}

// Get returns a shallow copy of the cached status if still fresh. Returns nil
// on miss. Callers may mutate the returned map without changing the cache.
func (c *statusCache) Get(key statusCacheKey) map[string]any {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Since(entry.fetchedAt) > c.ttl {
		return nil
	}
	out := make(map[string]any, len(entry.data))
	for key, value := range entry.data {
		out[key] = value
	}
	return out
}

// Set stores a fresh status response.
func (c *statusCache) Set(key statusCacheKey, data map[string]any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for key, entry := range c.entries {
		if now.Sub(entry.fetchedAt) > c.ttl {
			delete(c.entries, key)
		}
	}
	c.entries[key] = statusCacheEntry{data: data, fetchedAt: now}
}

// Invalidate clears the cache, forcing the next Get to return nil.
func (c *statusCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}
