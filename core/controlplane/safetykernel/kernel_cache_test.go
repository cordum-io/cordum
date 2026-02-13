package safetykernel

import (
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
