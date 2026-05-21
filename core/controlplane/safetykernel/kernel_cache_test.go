package safetykernel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
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

// TestResolveDecisionCacheMax exercises the non-positive guard. cacheMax<=0
// is a silent-data-loss path — zero disables the cache and pushes every
// decision to the evaluator, negative is a programmer typo that must never
// reach runtime — so both must fall through to the documented default.
func TestResolveDecisionCacheMax(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"positive override honored", "500", 500},
		{"zero falls back to default", "0", defaultDecisionCacheMaxSize},
		{"negative falls back to default", "-1", defaultDecisionCacheMaxSize},
		{"large negative falls back", "-2147483648", defaultDecisionCacheMaxSize},
		{"invalid int falls back", "not-a-number", defaultDecisionCacheMaxSize},
		{"empty falls back", "", defaultDecisionCacheMaxSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envDecisionCacheMaxSize, tc.raw)
			if got := resolveDecisionCacheMax(); got != tc.want {
				t.Fatalf("resolveDecisionCacheMax(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
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
	_ = srv.setPolicy(context.Background(), policyA, "snapA")

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
	_ = srv.setPolicy(context.Background(), policyB, "snapB")

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
	_ = srv.setPolicy(context.Background(), policy, "snap1")

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
		_ = srv.setPolicy(context.Background(), nil, fmt.Sprintf("snap%d", i))
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
	_ = srv.setPolicy(context.Background(), nil, "snap1") // version = 1

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
	_ = srv.setPolicy(context.Background(), nil, "snap2") // version = 2, cache cleared

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
	_ = srv.setPolicy(context.Background(), nil, "snap1") // version = 1

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
	_ = srv.setPolicy(context.Background(), nil, "snap2")

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
	_ = srv.setPolicy(context.Background(), nil, "snap1") // version = 1

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

func TestCachedDecisionImmutable(t *testing.T) {
	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	key := "immutable-key"
	resp := &pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:   "original",
		Remediations: []*pb.PolicyRemediation{
			{Id: "r1", Title: "original-title"},
		},
	}
	srv.setCachedDecision(key, resp)

	// Get cached entry and mutate it.
	got1 := srv.getCachedDecision(key)
	if got1 == nil {
		t.Fatal("expected cached decision")
	}
	got1.Decision = pb.DecisionType_DECISION_TYPE_DENY
	got1.Reason = "mutated"
	if len(got1.Remediations) > 0 {
		got1.Remediations[0].Title = "mutated-title"
	}

	// Get again — must return original, unaffected by mutation.
	got2 := srv.getCachedDecision(key)
	if got2 == nil {
		t.Fatal("expected cached decision on second get")
	}
	if got2.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("cached decision mutated: got %v, want ALLOW", got2.Decision)
	}
	if got2.Reason != "original" {
		t.Fatalf("cached reason mutated: got %q, want %q", got2.Reason, "original")
	}
	if len(got2.Remediations) > 0 && got2.Remediations[0].Title != "original-title" {
		t.Fatalf("cached remediation mutated: got %q, want %q", got2.Remediations[0].Title, "original-title")
	}
}

func TestCacheBypassedForVelocityPolicies(t *testing.T) {
	// When the active policy has velocity rules, caching must be entirely bypassed
	// so that the sliding window advances correctly on every request.
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	policy := &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{
			{
				ID:       "velocity-rule",
				Match:    config.PolicyMatch{Topics: []string{"job.rate-limited"}},
				Velocity: &config.VelocityConfig{MaxRequests: 2, WindowSeconds: 60, Key: "labels.session_id"},
				Decision: "deny",
				Reason:   "rate limit hit",
			},
			{
				ID:       "allow-fallback",
				Match:    config.PolicyMatch{Topics: []string{"job.*"}},
				Decision: "allow",
				Reason:   "allowed",
			},
		},
	}

	srv := &server{
		cacheTTL:        5 * time.Minute,
		cache:           map[string]cacheEntry{},
		cacheMaxSize:    100,
		resultClient:    client,
		velocityChecker: newVelocityChecker(client),
	}
	_ = srv.setPolicy(context.Background(), policy, "snap-cache-velocity")

	// Make 3 requests — first 2 should allow (within limit), 3rd should deny.
	for i := 1; i <= 3; i++ {
		req := &pb.PolicyCheckRequest{
			JobId:  fmt.Sprintf("job-cv-%d", i),
			Topic:  "job.rate-limited",
			Tenant: "default",
			Labels: map[string]string{"session_id": "sess-cv"},
		}
		resp, err := srv.evaluate(context.Background(), req, "check")
		if err != nil {
			t.Fatalf("request %d: error: %v", i, err)
		}
		if i <= 2 {
			if resp.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
				t.Fatalf("request %d: expected ALLOW, got %v", i, resp.Decision)
			}
		} else {
			if resp.Decision != pb.DecisionType_DECISION_TYPE_DENY {
				t.Fatalf("request %d: expected DENY (velocity exceeded), got %v — cache may have short-circuited velocity", i, resp.Decision)
			}
		}
	}

	// Cache should remain empty — velocity policies bypass caching.
	srv.cacheMu.Lock()
	size := len(srv.cache)
	srv.cacheMu.Unlock()
	if size != 0 {
		t.Fatalf("expected cache to be empty (velocity policies bypass caching), got %d entries", size)
	}
}

func TestCacheStillWorksForNonVelocityPolicies(t *testing.T) {
	// Caching must continue working for policies without velocity rules.
	policy := &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "allow",
	}
	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	_ = srv.setPolicy(context.Background(), policy, "snap-no-velocity")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-cached",
		Topic:  "job.test",
		Tenant: "default",
	}
	// First call evaluates and caches.
	resp1, err := srv.evaluate(context.Background(), req, "check")
	if err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	if resp1.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected ALLOW, got %v", resp1.Decision)
	}

	// Cache should have an entry.
	srv.cacheMu.Lock()
	size := len(srv.cache)
	srv.cacheMu.Unlock()
	if size != 1 {
		t.Fatalf("expected 1 cache entry for non-velocity policy, got %d", size)
	}

	// Second call should hit cache (different job_id, same topic/tenant/labels).
	req.JobId = "job-cached-2"
	resp2, err := srv.evaluate(context.Background(), req, "check")
	if err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if resp2.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected ALLOW from cache, got %v", resp2.Decision)
	}
}

func TestDecisionCacheBypassesActionDescriptorRequests(t *testing.T) {
	policy := &config.SafetyPolicy{DefaultTenant: "default", DefaultDecision: "allow"}
	srv := &server{cacheTTL: 5 * time.Minute, cache: map[string]cacheEntry{}, cacheMaxSize: 100}
	_ = srv.setPolicy(context.Background(), policy, "snap-action-cache")
	gate := &cacheActionGate{}
	gate.set(actiongates.ActionGateDecision{
		Decision: pb.DecisionType_DECISION_TYPE_ALLOW,
		GateID:   "actiongate.cache-test",
		Code:     "allow",
		Reason:   "initial allow",
	})
	srv.SetActionDescriptorExtractor(actionDescriptorFromRequest)
	srv.SetActionGatePipeline(actiongates.NewPipeline(gate))
	req := actionDescriptorCacheRequest(t, "job-action-1")

	resp1, err := srv.evaluate(context.Background(), req, "check")
	if err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	if resp1.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("first decision = %v, want ALLOW", resp1.GetDecision())
	}
	if got := cacheSize(srv); got != 0 {
		t.Fatalf("action descriptor response was cached; entries=%d", got)
	}

	gate.set(actiongates.ActionGateDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_DENY,
		GateID:    "actiongate.cache-test",
		Code:      actiongates.CodeAccessDenied,
		Reason:    "dynamic state changed",
		SubReason: "approval_state_changed",
	})
	req.JobId = "job-action-2"
	resp2, err := srv.evaluate(context.Background(), req, "check")
	if err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if resp2.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("second decision = %v, want DENY after dynamic gate changed", resp2.GetDecision())
	}
}

func TestConcurrentCacheReads(t *testing.T) {
	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
	}
	key := "concurrent-key"
	resp := &pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:   "concurrent-test",
	}
	srv.setCachedDecision(key, resp)

	var wg sync.WaitGroup
	errors := make(chan string, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := srv.getCachedDecision(key)
			if got == nil {
				errors <- "expected cached decision"
				return
			}
			if got.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
				errors <- fmt.Sprintf("unexpected decision: %v", got.Decision)
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

type cacheActionGate struct {
	mu  sync.Mutex
	dec actiongates.ActionGateDecision
}

func (g *cacheActionGate) ID() string { return "actiongate.cache-test" }

func (g *cacheActionGate) set(dec actiongates.ActionGateDecision) {
	g.mu.Lock()
	g.dec = dec
	g.mu.Unlock()
}

func (g *cacheActionGate) Evaluate(context.Context, *config.PolicyInput) actiongates.ActionGateDecision {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.dec
}

func actionDescriptorCacheRequest(t *testing.T, jobID string) *pb.PolicyCheckRequest {
	t.Helper()
	desc := config.ActionDescriptor{Kind: config.ActionKindMutation, Verb: config.ActionVerbDelete}
	raw, err := json.Marshal(desc)
	if err != nil {
		t.Fatalf("marshal action descriptor: %v", err)
	}
	return &pb.PolicyCheckRequest{
		JobId:  jobID,
		Topic:  "job.test",
		Tenant: "default",
		Labels: map[string]string{LabelActionDescriptorJSON: string(raw)},
	}
}

func cacheSize(srv *server) int {
	srv.cacheMu.Lock()
	defer srv.cacheMu.Unlock()
	return len(srv.cache)
}

func TestPolicyCacheTOCTOU(t *testing.T) {
	// Verify that concurrent policy reload + evaluation never returns
	// a stale cached decision. This is the TOCTOU scenario: if the
	// policyVersion is read before cacheMu, a reload between the read
	// and lock acquisition could cause a stale version match.

	policyAllow := &config.SafetyPolicy{
		Version:         "v1",
		DefaultDecision: "allow",
	}
	policyDeny := &config.SafetyPolicy{
		Version:         "v2",
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{
			{Match: config.PolicyMatch{Topics: []string{"job.test"}}, Decision: "deny", Reason: "blocked"},
		},
	}

	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 1000,
	}
	_ = srv.setPolicy(context.Background(), policyAllow, "snap-allow")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-toctou",
		Topic:  "job.test",
		Tenant: "default",
	}

	// Warm the cache with an ALLOW decision under policyAllow.
	resp, err := srv.evaluate(context.Background(), req, "check")
	if err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	if resp.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected initial ALLOW, got %v", resp.Decision)
	}

	// Now reload to policyDeny while concurrent readers are hitting the cache.
	var wg sync.WaitGroup
	staleCount := make(chan int, 1)
	stale := 0

	// Start 10 reader goroutines that evaluate continuously.
	stop := make(chan struct{})
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = srv.evaluate(context.Background(), req, "check")
				}
			}
		}()
	}

	// Reload policy to deny.
	_ = srv.setPolicy(context.Background(), policyDeny, "snap-deny")

	// After reload, all evaluations must return DENY — no stale ALLOW.
	for i := 0; i < 100; i++ {
		r, err := srv.evaluate(context.Background(), req, "check")
		if err != nil {
			t.Fatalf("post-reload evaluate: %v", err)
		}
		if r.Decision == pb.DecisionType_DECISION_TYPE_ALLOW {
			stale++
		}
	}

	close(stop)
	wg.Wait()
	staleCount <- stale

	if s := <-staleCount; s > 0 {
		t.Fatalf("TOCTOU: %d/100 evaluations returned stale ALLOW after policy reload to DENY", s)
	}
}
