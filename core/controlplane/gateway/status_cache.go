package gateway

import (
	"sync"
	"time"
)

// statusCache caches the /api/v1/status response to avoid repeated Redis
// PING + snapshot reads + pipeline counts on every dashboard poll.
type statusCache struct {
	mu        sync.RWMutex
	data      map[string]any
	fetchedAt time.Time
	ttl       time.Duration
}

func newStatusCache(ttl time.Duration) *statusCache {
	if ttl <= 0 {
		ttl = 2 * time.Second
	}
	return &statusCache{ttl: ttl}
}

// Get returns a shallow copy of the cached status if still fresh. Returns nil
// on miss. Callers may mutate the returned map without changing the cache.
func (c *statusCache) Get() map[string]any {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.data == nil || time.Since(c.fetchedAt) > c.ttl {
		return nil
	}
	out := make(map[string]any, len(c.data))
	for key, value := range c.data {
		out[key] = value
	}
	return out
}

// Set stores a fresh status response.
func (c *statusCache) Set(data map[string]any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = data
	c.fetchedAt = time.Now()
}

// Invalidate clears the cache, forcing the next Get to return nil.
func (c *statusCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = nil
	c.fetchedAt = time.Time{}
}
