package safetykernel

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

// TestDecisionCacheBypassedForAgentRequests reproduces the stale-ALLOW-after-
// escalation bug: cacheKeyForRequest omits the store-resolved agent identity
// (RiskTier/classification enriched AFTER the key is computed), so a cached
// ALLOW from when the agent was low-risk is served even though the agent is now
// escalated and the policy should now require approval / deny.
func TestDecisionCacheBypassedForAgentRequests(t *testing.T) {
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mini.Close)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	agentStore := store.NewAgentIdentityStoreFromClient(client)
	// The agent is currently escalated to "critical" in the store.
	if _, err := agentStore.Create(context.Background(), store.AgentIdentity{
		ID:       "agent-esc",
		Name:     "escalated-agent",
		Owner:    "admin",
		RiskTier: "critical",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	policy := &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "allow",
		Rules: []config.PolicyRule{
			{
				ID:       "critical-agent-approval",
				Match:    config.PolicyMatch{Topics: []string{"job.*"}, AgentRiskTiers: []string{"high", "critical"}},
				Decision: "require_approval",
				Reason:   "High/critical risk agents require approval",
			},
		},
	}

	srv := &server{
		cacheTTL:      5 * time.Minute,
		cache:         map[string]cacheEntry{},
		cacheMaxSize:  100,
		agentStore:    agentStore,
		agentCacheTTL: defaultAgentCacheTTL,
	}
	_ = srv.setPolicy(context.Background(), policy, "snap-agent")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-esc-1",
		Topic:  "job.process",
		Tenant: "default",
		Labels: map[string]string{"agent_id": "agent-esc"},
	}

	// Seed a STALE cached ALLOW under the agent-blind key (what a prior eval
	// would have cached when the agent was low-risk). The bug: the key collides
	// with the now-escalated request's key, so this is served verbatim.
	srv.setCachedDecision(
		cacheKeyForRequest(req, srv.snapshot),
		&pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "stale allow"},
	)

	resp, err := srv.evaluate(context.Background(), req, "check")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	// The escalated agent must NOT be served the stale cached ALLOW — the
	// decision must reflect the current (critical) identity.
	if resp.GetDecision() == pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("escalated agent served the stale cached ALLOW (reason=%q) — decision cache must not key out the resolved agent identity", resp.GetReason())
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		t.Fatalf("expected REQUIRE_HUMAN for the escalated (critical) agent, got %v", resp.GetDecision())
	}
}

func TestRequestHasAgentID(t *testing.T) {
	if requestHasAgentID(nil) {
		t.Fatalf("nil request → false")
	}
	if requestHasAgentID(&pb.PolicyCheckRequest{}) {
		t.Fatalf("no labels → false")
	}
	if requestHasAgentID(&pb.PolicyCheckRequest{Labels: map[string]string{"agent_id": "   "}}) {
		t.Fatalf("blank agent_id → false")
	}
	if !requestHasAgentID(&pb.PolicyCheckRequest{Labels: map[string]string{"agent_id": "agent-1"}}) {
		t.Fatalf("present agent_id → true")
	}
}

// TestDecisionCacheNotPopulatedForAgentRequests asserts an agent_id request with
// a store wired never writes the decision cache (so a later escalation can't be
// masked by a stale entry).
func TestDecisionCacheNotPopulatedForAgentRequests(t *testing.T) {
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mini.Close)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	agentStore := store.NewAgentIdentityStoreFromClient(client)
	if _, err := agentStore.Create(context.Background(), store.AgentIdentity{ID: "agent-low", Name: "low", Owner: "admin", RiskTier: "low"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	srv := &server{
		cacheTTL:      5 * time.Minute,
		cache:         map[string]cacheEntry{},
		cacheMaxSize:  100,
		agentStore:    agentStore,
		agentCacheTTL: defaultAgentCacheTTL,
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{DefaultTenant: "default", DefaultDecision: "allow"}, "snap")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-low-1",
		Topic:  "job.process",
		Tenant: "default",
		Labels: map[string]string{"agent_id": "agent-low"},
	}
	if _, err := srv.evaluate(context.Background(), req, "check"); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got := cacheSize(srv); got != 0 {
		t.Fatalf("agent request must not populate the decision cache, got %d entries", got)
	}
}

// TestDecisionCacheAgentLabelInertWithoutStore asserts the agent_id label is
// inert (and caching is preserved) when no agent store is wired — so the bypass
// doesn't regress hit-rate for non-enriched deployments.
func TestDecisionCacheAgentLabelInertWithoutStore(t *testing.T) {
	srv := &server{
		cacheTTL:     5 * time.Minute,
		cache:        map[string]cacheEntry{},
		cacheMaxSize: 100,
		// agentStore intentionally nil — enrichment off.
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{DefaultTenant: "default", DefaultDecision: "allow"}, "snap")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-inert-1",
		Topic:  "job.test",
		Tenant: "default",
		Labels: map[string]string{"agent_id": "agent-x"},
	}
	if _, err := srv.evaluate(context.Background(), req, "check"); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got := cacheSize(srv); got != 1 {
		t.Fatalf("agent_id label must be inert without a store (caching preserved), got %d entries", got)
	}
}
