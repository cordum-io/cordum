package agentd

import (
	"strings"
	"sync"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

// SafeAllowCache is the optional, bounded in-memory cache for the agentd
// evaluator. It exists so that a busy local hook flow (e.g. running a test
// suite that hits the same `npm test` Bash 50 times in 30 seconds) does not
// generate 50 Gateway evaluate calls when the only honest answer is "this
// known-safe action is allowed and the policy snapshot has not moved."
//
// The cache MUST NOT be used for:
//   - DENY, REQUIRE_APPROVAL, CONSTRAIN, or THROTTLE outcomes — only ALLOW
//   - high-risk actions (destructive, secrets, network, deploy, mutating)
//   - unknown/review_required actions (classifier said "I don't know")
//   - approval-derived ALLOW (consumed once is consumed once; never replayed)
//   - degraded/safety-unavailable allows (no fresh decision was obtained)
//   - entries whose policy_mode, policy_snapshot, action_hash, or input_hash
//     have moved since insertion
//
// A cache hit still writes a bounded evidence event so the audit trail stays
// honest about how many actions actually ran. The wire fact "Gateway said
// ALLOW for this exact (action_hash, input_hash, policy_snapshot) at T-15s"
// is recorded once; the replay events reference that decision.
type SafeAllowCache struct {
	mu      sync.Mutex
	entries map[string]safeAllowEntry
	order   []string
	max     int
	ttl     time.Duration
	clock   func() time.Time
}

// safeAllowEntry is the cached record. Fields are deliberately minimal — no
// raw payloads, no approval references, no transcript content — so the cache
// itself cannot leak secrets even if dumped to a debug log.
type safeAllowEntry struct {
	Reason         string
	RuleID         string
	PolicySnapshot string
	ActionHash     string
	InputHash      string
	ExpiresAt      time.Time
	InsertedAt     time.Time
}

// SafeAllowCacheConfig configures the optional in-memory cache. A zero
// MaxEntries disables the cache entirely; agentd must construct it via
// NewSafeAllowCache(0, ...) only when the operator opts in via config.
type SafeAllowCacheConfig struct {
	MaxEntries int
	TTL        time.Duration
	Clock      func() time.Time
}

// NewSafeAllowCache returns nil when MaxEntries <= 0 so callers can use a nil
// receiver as "cache disabled" without nil-checking the cfg struct everywhere.
func NewSafeAllowCache(cfg SafeAllowCacheConfig) *SafeAllowCache {
	if cfg.MaxEntries <= 0 {
		return nil
	}
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &SafeAllowCache{
		entries: make(map[string]safeAllowEntry, cfg.MaxEntries),
		order:   make([]string, 0, cfg.MaxEntries),
		max:     cfg.MaxEntries,
		ttl:     cfg.TTL,
		clock:   clock,
	}
}

// SafeAllowKey identifies a cacheable action. The cache is invalidated by any
// difference in TenantID / PolicyMode / PolicySnapshot / ActionHash / InputHash,
// which is what prevents a stale ALLOW from outliving a policy change or a
// principal switch.
type SafeAllowKey struct {
	TenantID       string
	PolicyMode     edgecore.PolicyMode
	PolicySnapshot string
	ActionHash     string
	InputHash      string
}

func (k SafeAllowKey) compositeKey() string {
	return strings.Join([]string{
		strings.TrimSpace(k.TenantID),
		strings.TrimSpace(string(k.PolicyMode)),
		strings.TrimSpace(k.PolicySnapshot),
		strings.TrimSpace(k.ActionHash),
		strings.TrimSpace(k.InputHash),
	}, "\x1f")
}

// SafeAllowEligibility is the set of preconditions agentd must verify before
// inserting (or consulting) a cache entry. The classifier's risk_tags drive
// the IsRiskTagSafe check; the evaluator's outcome drives IsAllowed.
type SafeAllowEligibility struct {
	IsAllowed       bool     // Gateway returned a fresh ALLOW (not degraded, not approval-derived)
	IsKnownSafe     bool     // classifier produced a known-safe action_name + capability
	RiskTags        []string // classifier risk_tags; high-risk tags veto cache
	HasApprovalRef  bool     // ApprovalRef was consumed; never cache approval-derived allows
	WasDegraded     bool     // safety_unavailable / malformed / etc — never cache
}

// EligibleForCache returns whether (key, e) may be stored. Negative checks
// dominate: any high-risk tag, any approval involvement, any degraded hint
// vetoes caching even if the classifier said "known safe."
func (e SafeAllowEligibility) EligibleForCache() bool {
	if !e.IsAllowed || e.WasDegraded || e.HasApprovalRef || !e.IsKnownSafe {
		return false
	}
	for _, tag := range e.RiskTags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "destructive", "secrets", "network", "deploy", "mutating", "write", "filesystem", "unknown", "review_required":
			return false
		}
	}
	return true
}

// Get looks up a cached safe-allow entry. Returns (entry, true) on hit, with a
// non-expired entry; returns (zero, false) on miss or if the cache is nil.
func (c *SafeAllowCache) Get(key SafeAllowKey) (safeAllowEntry, bool) {
	if c == nil {
		return safeAllowEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	composite := key.compositeKey()
	entry, ok := c.entries[composite]
	if !ok {
		return safeAllowEntry{}, false
	}
	if !entry.ExpiresAt.IsZero() && c.clock().After(entry.ExpiresAt) {
		c.removeLocked(composite)
		return safeAllowEntry{}, false
	}
	return entry, true
}

// Put inserts an entry. Eligibility MUST be checked by the caller via
// EligibleForCache; Put trusts that the caller did so. When the cache is at
// capacity, Put evicts the oldest entry (FIFO; not strict LRU because agentd
// rebuilds the cache on every restart so cold-start churn is the only loss).
//
// Put is a no-op when c is nil so callers can write `cache.Put(...)` even
// when caching is disabled.
func (c *SafeAllowCache) Put(key SafeAllowKey, entry safeAllowEntry) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	composite := key.compositeKey()
	if entry.InsertedAt.IsZero() {
		entry.InsertedAt = c.clock()
	}
	if entry.ExpiresAt.IsZero() && c.ttl > 0 {
		entry.ExpiresAt = entry.InsertedAt.Add(c.ttl)
	}
	if _, exists := c.entries[composite]; !exists {
		if len(c.entries) >= c.max && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		c.order = append(c.order, composite)
	}
	c.entries[composite] = entry
}

// InvalidateTenant removes all entries for a tenant. Used when an operator
// rotates a principal/tenant or a session ends with a degraded state and
// agentd wants to flush the cache to force fresh Gateway decisions.
func (c *SafeAllowCache) InvalidateTenant(tenantID string) int {
	if c == nil {
		return 0
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := tenantID + "\x1f"
	removed := 0
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			c.removeLocked(k)
			removed++
		}
	}
	return removed
}

// Len reports the current entry count (post-eviction). Useful for tests and
// metrics; agentd never makes decisions based on this.
func (c *SafeAllowCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// removeLocked must be called with c.mu held.
func (c *SafeAllowCache) removeLocked(composite string) {
	delete(c.entries, composite)
	for i, k := range c.order {
		if k == composite {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
