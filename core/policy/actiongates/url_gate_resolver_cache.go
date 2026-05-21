package actiongates

import (
	"container/list"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

var errUnexpectedResolverResult = errors.New("unexpected resolver result")
var errResolverNoAddresses = errors.New("resolver returned no addresses")
var errResolverInvalidAddress = errors.New("resolver returned invalid address")

type resolverLookupResult struct {
	ips    []net.IP
	cached bool
}

type urlGateResolverCache struct {
	mu      sync.Mutex
	max     int
	entries map[string]*list.Element
	lru     *list.List
}

type urlGateResolverCacheEntry struct {
	key    string
	ips    []net.IP
	expiry time.Time
}

func newURLGateResolverCache(maxEntries int) *urlGateResolverCache {
	return &urlGateResolverCache{
		max:     maxEntries,
		entries: make(map[string]*list.Element),
		lru:     list.New(),
	}
}

func (c *urlGateResolverCache) get(key string, now time.Time) ([]net.IP, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	entry := elem.Value.(*urlGateResolverCacheEntry)
	if !now.Before(entry.expiry) {
		c.removeLocked(elem)
		return nil, false
	}
	c.lru.MoveToFront(elem)
	return cloneResolverIPs(entry.ips), true
}

func (c *urlGateResolverCache) put(key string, ips []net.IP, expiry time.Time) int {
	if c.max <= 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		entry := elem.Value.(*urlGateResolverCacheEntry)
		entry.ips = cloneResolverIPs(ips)
		entry.expiry = expiry
		c.lru.MoveToFront(elem)
		return 0
	}
	entry := &urlGateResolverCacheEntry{key: key, ips: cloneResolverIPs(ips), expiry: expiry}
	c.entries[key] = c.lru.PushFront(entry)
	evictions := 0
	for len(c.entries) > c.max {
		if elem := c.lru.Back(); elem != nil {
			c.removeLocked(elem)
			evictions++
		}
	}
	return evictions
}

func (c *urlGateResolverCache) removeLocked(elem *list.Element) {
	entry := elem.Value.(*urlGateResolverCacheEntry)
	delete(c.entries, entry.key)
	c.lru.Remove(elem)
}

func normalizeResolverCacheKey(host string) string {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return ""
	}
	if parsedHost, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = parsedHost
	}
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.LastIndex(trimmed, "]"); end > 0 {
			trimmed = trimmed[1:end]
		}
	}
	return strings.ToLower(trimmed)
}

func cloneResolverIPs(in []net.IP) []net.IP {
	if in == nil {
		return nil
	}
	out := make([]net.IP, len(in))
	for i, ip := range in {
		out[i] = cloneResolverIP(ip)
	}
	return out
}

func cloneResolverIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	out := make(net.IP, len(ip))
	copy(out, ip)
	return out
}
