package safetykernel

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestCacheKeyForRequestStable(t *testing.T) {
	req := &pb.PolicyCheckRequest{
		JobId:  "job-1",
		Topic:  "job.test",
		Tenant: "tenant",
		Labels: map[string]string{"k": "v"},
	}
	key1 := cacheKeyForRequest(req, "snap")
	req.JobId = "job-2"
	key2 := cacheKeyForRequest(req, "snap")
	if key1 == "" || key2 == "" {
		t.Fatalf("expected non-empty cache keys")
	}
	if key1 != key2 {
		t.Fatalf("expected cache key to ignore job id")
	}
	if cacheKeyForRequest(nil, "snap") != "" {
		t.Fatalf("expected empty cache key for nil request")
	}
}

func TestDecisionCacheRespectsTTL(t *testing.T) {
	srv := &server{
		cacheTTL: 25 * time.Millisecond,
		cache:    map[string]cacheEntry{},
	}
	key := "cache-key"
	resp := &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}
	srv.setCachedDecision(key, resp)
	if got := srv.getCachedDecision(key); got == nil {
		t.Fatalf("expected cached decision")
	}
	// Poll until the TTL expires and the cache entry is evicted.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.getCachedDecision(key) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.getCachedDecision(key); got != nil {
		t.Fatalf("expected cached decision to expire")
	}
}

func TestClonePolicyResponseIsolated(t *testing.T) {
	orig := &pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:   "ok",
		Remediations: []*pb.PolicyRemediation{
			{Id: "r1", Title: "fix"},
		},
	}
	clone := clonePolicyResponse(orig)
	if clone == orig {
		t.Fatalf("expected clone to be distinct")
	}
	clone.Decision = pb.DecisionType_DECISION_TYPE_DENY
	clone.Remediations[0].Title = "changed"
	if orig.Decision == clone.Decision || orig.Remediations[0].Title == "changed" {
		t.Fatalf("expected clone to be isolated")
	}
}

func TestParseDurationEnv(t *testing.T) {
	t.Setenv(envDecisionCacheTTL, "2s")
	if got := parseDurationEnv(envDecisionCacheTTL); got != 2*time.Second {
		t.Fatalf("expected duration 2s, got %s", got)
	}
	t.Setenv(envDecisionCacheTTL, "bad")
	if got := parseDurationEnv(envDecisionCacheTTL); got != 0 {
		t.Fatalf("expected invalid duration to return 0, got %s", got)
	}
}

func TestHostAllowed(t *testing.T) {
	allow := []string{"example.com", ".trusted.local"}
	if !hostAllowed("api.example.com", allow) {
		t.Fatalf("expected subdomain to be allowed")
	}
	if hostAllowed("evil.com", allow) {
		t.Fatalf("expected host to be blocked")
	}
}

func TestValidatePolicyURL(t *testing.T) {
	u, err := url.Parse("https://8.8.8.8/policy")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if err := validatePolicyURL(u); err != nil {
		t.Fatalf("expected public ip to be allowed: %v", err)
	}

	privateURL, _ := url.Parse("http://127.0.0.1/policy")
	if err := validatePolicyURL(privateURL); err == nil {
		t.Fatalf("expected loopback host to be rejected")
	}

	t.Setenv("SAFETY_POLICY_URL_ALLOWLIST", "example.com")
	t.Setenv("SAFETY_POLICY_URL_ALLOW_PRIVATE", "true")
	allowedURL, _ := url.Parse("https://api.example.com/policy")
	if err := validatePolicyURL(allowedURL); err != nil {
		t.Fatalf("expected allowlisted host to pass: %v", err)
	}
}

func TestMergeMCPPolicy(t *testing.T) {
	base := config.MCPPolicy{AllowServers: []string{"a"}, DenyTools: []string{"x"}}
	extra := config.MCPPolicy{AllowServers: []string{"b"}, DenyTools: []string{"y"}, AllowActions: []string{"read"}}
	merged := mergeMCPPolicy(base, extra)
	if len(merged.AllowServers) != 2 || merged.AllowServers[0] != "a" || merged.AllowServers[1] != "b" {
		t.Fatalf("unexpected merged allow servers")
	}
	if len(merged.DenyTools) != 2 || merged.DenyTools[1] != "y" {
		t.Fatalf("unexpected merged deny tools")
	}
	if len(merged.AllowActions) != 1 || merged.AllowActions[0] != "read" {
		t.Fatalf("unexpected merged allow actions")
	}
}

func TestDecisionCacheEvictsWhenFull(t *testing.T) {
	srv := &server{
		cacheTTL:     time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 3,
	}
	for i := 0; i < 4; i++ {
		key := fmt.Sprintf("key-%d", i)
		resp := &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: key}
		srv.setCachedDecision(key, resp)
		// Small sleep ensures distinct expires timestamps for deterministic eviction order.
		time.Sleep(time.Millisecond)
	}
	srv.cacheMu.Lock()
	size := len(srv.cache)
	srv.cacheMu.Unlock()
	if size != 3 {
		t.Fatalf("expected cache size 3, got %d", size)
	}
	// Newest entry must be present.
	if got := srv.getCachedDecision("key-3"); got == nil {
		t.Fatalf("expected newest entry key-3 to be present")
	}
	// Oldest entry (key-0) should have been evicted (earliest expires).
	if got := srv.getCachedDecision("key-0"); got != nil {
		t.Fatalf("expected oldest entry key-0 to be evicted")
	}
}

func TestDecisionCacheEvictsExpiredFirst(t *testing.T) {
	srv := &server{
		cacheTTL:     25 * time.Millisecond,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 2,
	}
	resp := &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}
	srv.setCachedDecision("old-1", resp)
	srv.setCachedDecision("old-2", resp)

	// Wait for TTL to expire.
	time.Sleep(50 * time.Millisecond)

	// Insert a new entry — expired entries should be swept first.
	srv.setCachedDecision("new-1", resp)

	srv.cacheMu.Lock()
	size := len(srv.cache)
	srv.cacheMu.Unlock()
	if size > 2 {
		t.Fatalf("expected cache size <= 2, got %d", size)
	}
	if got := srv.getCachedDecision("new-1"); got == nil {
		t.Fatalf("expected new entry to be retrievable")
	}
}

func TestDecisionCacheMaxSizeEnvParsing(t *testing.T) {
	t.Setenv(envDecisionCacheMaxSize, "500")
	if got := parseIntEnv(envDecisionCacheMaxSize, defaultDecisionCacheMaxSize); got != 500 {
		t.Fatalf("expected 500, got %d", got)
	}

	t.Setenv(envDecisionCacheMaxSize, "not-a-number")
	if got := parseIntEnv(envDecisionCacheMaxSize, defaultDecisionCacheMaxSize); got != defaultDecisionCacheMaxSize {
		t.Fatalf("expected default %d for invalid input, got %d", defaultDecisionCacheMaxSize, got)
	}

	t.Setenv(envDecisionCacheMaxSize, "")
	if got := parseIntEnv(envDecisionCacheMaxSize, defaultDecisionCacheMaxSize); got != defaultDecisionCacheMaxSize {
		t.Fatalf("expected default %d for empty input, got %d", defaultDecisionCacheMaxSize, got)
	}
}

func TestDecisionCacheTTLPreservedWithBound(t *testing.T) {
	srv := &server{
		cacheTTL:     25 * time.Millisecond,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	key := "bounded-ttl-key"
	resp := &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}
	srv.setCachedDecision(key, resp)
	if got := srv.getCachedDecision(key); got == nil {
		t.Fatalf("expected cached decision")
	}
	// Poll until TTL expires.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.getCachedDecision(key) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.getCachedDecision(key); got != nil {
		t.Fatalf("expected cached decision to expire with bounded cache")
	}
}

// ---------------------------------------------------------------------------
// Versioned cache invalidation tests
// ---------------------------------------------------------------------------

func TestCacheInvalidatedOnPolicyChange(t *testing.T) {
	// Policy A: allow everything (default allow).
	policyA := &config.SafetyPolicy{
		Version:         "v1",
		DefaultDecision: "allow",
	}
	// Policy B: deny topic "job.test".
	policyB := &config.SafetyPolicy{
		Version:         "v2",
		DefaultDecision: "allow",
		Rules: []config.PolicyRule{
			{Match: config.PolicyMatch{Topics: []string{"job.test"}}, Decision: "deny", Reason: "blocked by v2"},
		},
	}

	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	srv.setPolicy(policyA, "snapA")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-1",
		Topic:  "job.test",
		Tenant: "default",
	}

	// First evaluation under policy A — should allow.
	resp1, err := srv.evaluate(context.Background(), req, "check")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if resp1.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected ALLOW under policy A, got %v", resp1.Decision)
	}

	// Change to policy B — should invalidate cache.
	srv.setPolicy(policyB, "snapB")

	// Second evaluation with same request — must see DENY (not stale cache).
	resp2, err := srv.evaluate(context.Background(), req, "check")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if resp2.Decision != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected DENY under policy B, got %v (stale cache served)", resp2.Decision)
	}
	if resp2.Reason != "blocked by v2" {
		t.Fatalf("expected reason from policy B, got %q", resp2.Reason)
	}
}

func TestCacheHitSamePolicyVersion(t *testing.T) {
	policy := &config.SafetyPolicy{
		Version:         "v1",
		DefaultDecision: "allow",
	}
	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	srv.setPolicy(policy, "snap1")

	key := "test-key"
	resp := &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}
	srv.setCachedDecision(key, resp)

	// Same policy version — should return cached entry.
	got := srv.getCachedDecision(key)
	if got == nil {
		t.Fatal("expected cache hit for same policy version")
	}
	if got.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected ALLOW, got %v", got.Decision)
	}
}

func TestSetPolicyBumpsVersion(t *testing.T) {
	srv := &server{
		cacheTTL:     time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}

	// Initial version is 0 (zero value).
	if v := srv.policyVersion.Load(); v != 0 {
		t.Fatalf("expected initial version 0, got %d", v)
	}

	// Each setPolicy call bumps version by 1.
	for i := uint64(1); i <= 5; i++ {
		srv.setPolicy(nil, fmt.Sprintf("snap%d", i))
		if v := srv.policyVersion.Load(); v != i {
			t.Fatalf("expected version %d after %d calls, got %d", i, i, v)
		}
	}
}

func TestCacheEntriesCarryVersion(t *testing.T) {
	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	srv.setPolicy(nil, "snap1") // version = 1

	key := "versioned-key"
	resp := &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}
	srv.setCachedDecision(key, resp)

	// Verify entry has version 1.
	srv.cacheMu.Lock()
	entry, ok := srv.cache[key]
	srv.cacheMu.Unlock()
	if !ok {
		t.Fatal("expected cache entry")
	}
	if entry.policyVersion != 1 {
		t.Fatalf("expected entry version 1, got %d", entry.policyVersion)
	}

	// Bump to version 2.
	srv.setPolicy(nil, "snap2") // version = 2, cache cleared

	// The cache was cleared by setPolicy, so the entry should be gone.
	if got := srv.getCachedDecision(key); got != nil {
		t.Fatal("expected cache miss after policy change (cache cleared)")
	}

	// Re-add under version 2 and verify it's served.
	srv.setCachedDecision(key, resp)
	if got := srv.getCachedDecision(key); got == nil {
		t.Fatal("expected cache hit for current version")
	}
}

func TestCacheClearedOnSetPolicy(t *testing.T) {
	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	srv.setPolicy(nil, "snap1") // version = 1

	// Populate cache.
	for i := 0; i < 10; i++ {
		srv.setCachedDecision(
			fmt.Sprintf("key-%d", i),
			&pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW},
		)
	}

	srv.cacheMu.Lock()
	size := len(srv.cache)
	srv.cacheMu.Unlock()
	if size != 10 {
		t.Fatalf("expected 10 cached entries, got %d", size)
	}

	// Policy change should clear cache entirely.
	srv.setPolicy(nil, "snap2")

	srv.cacheMu.Lock()
	size = len(srv.cache)
	srv.cacheMu.Unlock()
	if size != 0 {
		t.Fatalf("expected 0 cached entries after policy change, got %d", size)
	}
}

func TestCacheVersionMismatchDeletesEntry(t *testing.T) {
	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	srv.setPolicy(nil, "snap1") // version = 1

	key := "stale-key"
	resp := &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}
	srv.setCachedDecision(key, resp)

	// Manually bump version without clearing cache (simulate a race window).
	srv.policyVersion.Add(1) // version = 2, but cache NOT cleared

	// getCachedDecision should detect version mismatch and delete the entry.
	if got := srv.getCachedDecision(key); got != nil {
		t.Fatal("expected cache miss for version mismatch")
	}

	// Entry should be deleted from the map.
	srv.cacheMu.Lock()
	_, exists := srv.cache[key]
	srv.cacheMu.Unlock()
	if exists {
		t.Fatal("expected stale entry to be deleted from cache map")
	}
}
