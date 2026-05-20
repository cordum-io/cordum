package agentd

import (
	"sort"
	"strings"
	"sync"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

const (
	defaultSafeAllowCacheTTL        = 5 * time.Minute
	defaultSafeAllowCacheMaxEntries = 128
)

// SafeAllowCacheConfig controls the optional in-memory safe ALLOW cache.
type SafeAllowCacheConfig struct {
	Enabled    bool
	TTL        time.Duration
	MaxEntries int
}

// SafeAllowCacheRequest is the normalized, non-secret action identity used for
// cache eligibility and lookup. InputRedacted is accepted so callers can pass
// the hook request shape directly, but it is deliberately never included in the
// cache key or stored entry.
type SafeAllowCacheRequest struct {
	TenantID                 string
	PolicyMode               edgecore.PolicyMode
	PolicySnapshot           string
	WorkflowOverrideSnapshot string
	JobOverrideSnapshot      string
	Kind                     string
	ActionName               string
	Capability               string
	RiskTags                 []string
	Labels                   map[string]string
	ActionHash               string
	InputHash                string
	ApprovalRef              string
	InputRedacted            map[string]any
}

type safeAllowCacheRecord struct {
	key       string
	response  EvaluateResponse
	expiresAt time.Time
}

// SafeAllowCache is a bounded, mutex-protected, in-memory cache for low-risk
// cache-eligible ALLOW decisions. It stores only sanitized decision metadata.
type SafeAllowCache struct {
	mu         sync.Mutex
	enabled    bool
	ttl        time.Duration
	maxEntries int
	clock      Clock
	records    map[string]safeAllowCacheRecord
	order      []string
}

func NewSafeAllowCache(cfg SafeAllowCacheConfig, clock Clock) *SafeAllowCache {
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultSafeAllowCacheTTL
	}
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultSafeAllowCacheMaxEntries
	}
	if clock == nil {
		clock = realClock{}
	}
	return &SafeAllowCache{
		enabled:    cfg.Enabled,
		ttl:        ttl,
		maxEntries: maxEntries,
		clock:      clock,
		records:    map[string]safeAllowCacheRecord{},
	}
}

func (c *SafeAllowCache) Get(req SafeAllowCacheRequest) (EvaluateResponse, bool) {
	if c == nil || !c.enabled || !safeAllowCacheRequestEligible(req) {
		return EvaluateResponse{}, false
	}
	key := safeAllowCacheKey(req)
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.records[key]
	if !ok {
		return EvaluateResponse{}, false
	}
	now := c.clock.Now().UTC()
	if !now.Before(record.expiresAt) {
		delete(c.records, key)
		c.removeOrderKeyLocked(key)
		return EvaluateResponse{}, false
	}
	return cloneEvaluateResponse(record.response), true
}

func (c *SafeAllowCache) Put(req SafeAllowCacheRequest, resp EvaluateResponse) bool {
	if c == nil || !c.enabled || !safeAllowCacheRequestEligible(req) || !safeAllowCacheResponseEligible(resp) {
		return false
	}
	key := safeAllowCacheKey(req)
	record := safeAllowCacheRecord{
		key:       key,
		response:  safeAllowCacheResponse(resp),
		expiresAt: c.clock.Now().UTC().Add(c.ttl),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.records[key]; exists {
		c.removeOrderKeyLocked(key)
	}
	c.order = append(c.order, key)
	c.records[key] = record
	c.evictOverflowLocked()
	return true
}

func (c *SafeAllowCache) evictOverflowLocked() {
	for len(c.records) > c.maxEntries && len(c.order) > 0 {
		victim := c.order[0]
		c.order = c.order[1:]
		delete(c.records, victim)
	}
}

func (c *SafeAllowCache) removeOrderKeyLocked(key string) {
	for i, existing := range c.order {
		if existing == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func safeAllowCacheRequestEligible(req SafeAllowCacheRequest) bool {
	if strings.TrimSpace(req.TenantID) == "" ||
		strings.TrimSpace(string(req.PolicyMode)) == "" ||
		strings.TrimSpace(req.PolicySnapshot) == "" ||
		strings.TrimSpace(req.Kind) == "" ||
		strings.TrimSpace(req.ActionHash) == "" ||
		strings.TrimSpace(req.InputHash) == "" ||
		strings.TrimSpace(req.ApprovalRef) != "" {
		return false
	}
	if strings.ToLower(strings.TrimSpace(req.Labels["command.class"])) != "safe" {
		return false
	}
	for _, tag := range req.RiskTags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "unknown", "review_required", "destructive", "network", "install", "deploy", "secrets", "mutating", "write":
			return false
		}
	}
	return true
}

func safeAllowCacheResponseEligible(resp EvaluateResponse) bool {
	if !resp.CacheEligible ||
		resp.Decision != string(edgecore.DecisionAllow) ||
		strings.ToLower(strings.TrimSpace(resp.PermissionDecision)) != "allow" ||
		strings.TrimSpace(resp.ApprovalRef) != "" ||
		resp.Degraded ||
		strings.TrimSpace(resp.ErrorCode) != "" {
		return false
	}
	return true
}

func safeAllowCacheKey(req SafeAllowCacheRequest) string {
	tags := append([]string(nil), req.RiskTags...)
	for i := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tags[i]))
	}
	sort.Strings(tags)
	parts := []string{
		strings.TrimSpace(req.TenantID),
		strings.TrimSpace(string(req.PolicyMode)),
		strings.TrimSpace(req.PolicySnapshot),
	}
	if strings.TrimSpace(req.WorkflowOverrideSnapshot) != "" || strings.TrimSpace(req.JobOverrideSnapshot) != "" {
		parts = append(parts,
			strings.TrimSpace(req.WorkflowOverrideSnapshot),
			strings.TrimSpace(req.JobOverrideSnapshot),
		)
	}
	parts = append(parts,
		strings.TrimSpace(req.Kind),
		strings.TrimSpace(req.ActionName),
		strings.TrimSpace(req.Capability),
		strings.Join(tags, ","),
		strings.ToLower(strings.TrimSpace(req.Labels["command.class"])),
		strings.ToLower(strings.TrimSpace(req.Labels["command.family"])),
		strings.TrimSpace(req.ActionHash),
		strings.TrimSpace(req.InputHash),
	)
	return strings.Join(parts, "\x00")
}

func safeAllowCacheResponse(resp EvaluateResponse) EvaluateResponse {
	out := cloneEvaluateResponse(resp)
	out.Reason = boundMetadataString(redactSecretLike(out.Reason))
	out.RuleID = boundMetadataString(out.RuleID)
	out.RuleTier = boundMetadataString(out.RuleTier)
	out.PolicySnapshot = boundMetadataString(out.PolicySnapshot)
	out.WorkflowOverrideSnapshot = boundMetadataString(out.WorkflowOverrideSnapshot)
	out.JobOverrideSnapshot = boundMetadataString(out.JobOverrideSnapshot)
	out.ActionHash = boundMetadataString(out.ActionHash)
	out.InputHash = boundMetadataString(out.InputHash)
	out.PermissionDecision = boundMetadataString(out.PermissionDecision)
	out.PermissionDecisionReason = boundMetadataString(redactSecretLike(out.PermissionDecisionReason))
	out.TerminalTitle = boundMetadataString(redactSecretLike(out.TerminalTitle))
	out.TerminalMessage = boundMetadataString(redactSecretLike(out.TerminalMessage))
	out.WaitStrategy = boundMetadataString(out.WaitStrategy)
	out.WaitAfter = boundMetadataString(out.WaitAfter)
	out.ErrorCode = ""
	out.ErrorMessage = ""
	out.ApprovalRef = ""
	out.ApprovalURL = ""
	out.UpdatedInput = nil
	out.Constraints = nil
	return out
}

func cloneEvaluateResponse(resp EvaluateResponse) EvaluateResponse {
	if resp.Constraints != nil {
		constraints := make(map[string]any, len(resp.Constraints))
		for k, v := range resp.Constraints {
			constraints[k] = v
		}
		resp.Constraints = constraints
	}
	if resp.UpdatedInput != nil {
		updated := make(map[string]any, len(resp.UpdatedInput))
		for k, v := range resp.UpdatedInput {
			updated[k] = v
		}
		resp.UpdatedInput = updated
	}
	return resp
}
