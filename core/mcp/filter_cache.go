package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// filterCacheTTL bounds how long a filtered tool slice stays in the
// cache. 60s matches the plan: identity changes must take effect on
// the next list request (the middleware refetches identity per HTTP
// request), and runtime-config changes invalidate the cache outright
// via configVersion, so this TTL only protects against unchanged
// identities hammering the filter loop.
const filterCacheTTL = 60 * time.Second

// filterCacheEntry holds a single pre-filtered tool slice with its
// absolute expiry timestamp. The slice is shared between callers;
// callers must not mutate it.
type filterCacheEntry struct {
	tools   []Tool
	expires time.Time
}

// filterCache memoises ListTools by identity + config_version. Entries
// expire after filterCacheTTL or when SetConfig bumps the version.
type filterCache struct {
	mu      sync.RWMutex
	entries map[string]filterCacheEntry
	version atomic.Uint64
}

// newFilterCache returns a ready-to-use cache. version starts at 1 so
// cache keys are never "v0" which would collide with "unknown".
func newFilterCache() *filterCache {
	c := &filterCache{entries: make(map[string]filterCacheEntry)}
	c.version.Store(1)
	return c
}

// bumpVersion invalidates every cached filter output. Called from
// SetConfig so a new policy takes effect on the next request without
// waiting for TTL expiry.
func (c *filterCache) bumpVersion() {
	if c == nil {
		return
	}
	c.version.Add(1)
}

// currentVersion exposes the cache version for external assertions
// (tests + diagnostic endpoints).
func (c *filterCache) currentVersion() uint64 {
	if c == nil {
		return 0
	}
	return c.version.Load()
}

// get returns the cached slice for the given identity at the current
// version, or (nil, false) on miss / expiry.
func (c *filterCache) get(id *AgentIdentity) ([]Tool, bool) {
	if c == nil {
		return nil, false
	}
	key := c.keyFor(id)
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expires) {
		// Expired — let the caller recompute. Stale entries are GC'd
		// opportunistically here.
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil, false
	}
	return entry.tools, true
}

// put stores the computed slice under the identity's key at the
// current config version. The slice is retained as-is and must not be
// mutated by callers after insertion.
func (c *filterCache) put(id *AgentIdentity, tools []Tool) {
	if c == nil {
		return
	}
	key := c.keyFor(id)
	c.mu.Lock()
	c.entries[key] = filterCacheEntry{
		tools:   tools,
		expires: time.Now().Add(filterCacheTTL),
	}
	// Cap the map so an attacker cycling synthetic agent-ids can't grow
	// it without bound. The cap is deliberately generous — under normal
	// usage an operator has at most hundreds of identities.
	const maxEntries = 10_000
	if len(c.entries) > maxEntries {
		// Oldest-first eviction. Cost is O(n) but only fires on overflow.
		var oldestKey string
		var oldest time.Time
		for k, e := range c.entries {
			if oldestKey == "" || e.expires.Before(oldest) {
				oldestKey, oldest = k, e.expires
			}
		}
		if oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}
	c.mu.Unlock()
}

// keyFor produces a stable cache key over everything that affects the
// filter output: identity fields + config version. A nil identity
// produces a deterministic "empty" key so the fail-closed result is
// shared across all nil-identity callers.
func (c *filterCache) keyFor(id *AgentIdentity) string {
	ver := c.version.Load()
	if id == nil {
		return "v" + u64ToString(ver) + "|nil"
	}
	// Snapshot both slices BEFORE iterating so a concurrent mutation of
	// the shared AgentIdentity (e.g. identity-store refresh on another
	// goroutine) doesn't race with keyFor. AllowedTools was already
	// copied; DataClassifications now matches.
	allowed := append([]string{}, id.AllowedTools...)
	sort.Strings(allowed)
	rawClasses := append([]string{}, id.DataClassifications...)
	classes := make([]string, 0, len(rawClasses))
	for _, c := range rawClasses {
		classes = append(classes, strings.ToLower(strings.TrimSpace(c)))
	}
	sort.Strings(classes)

	h := sha256.New()
	_, _ = h.Write([]byte(id.ID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(id.RiskTier))))
	_, _ = h.Write([]byte{0})
	for _, a := range allowed {
		_, _ = h.Write([]byte(a))
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write([]byte{1})
	for _, c := range classes {
		_, _ = h.Write([]byte(c))
		_, _ = h.Write([]byte{0})
	}
	return "v" + u64ToString(ver) + "|" + hex.EncodeToString(h.Sum(nil))
}

// u64ToString avoids an fmt/strconv import in the hot path.
func u64ToString(v uint64) string {
	if v == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for v > 0 {
		buf = append(buf, byte('0'+v%10))
		v /= 10
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
