package safetykernel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const isoCachedMarker = "served-from-decision-cache-for-tenant-a"

// isoTenantAllowPolicy allows job.iso.* for tenant-a only, so a decision
// computed for tenant-a is an ALLOW that tenant-b must never receive.
func isoTenantAllowPolicy() *config.SafetyPolicy {
	return &config.SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{{
			ID:       "iso-allow-tenant-a",
			Match:    config.PolicyMatch{Tenants: []string{"tenant-a"}, Topics: []string{"job.iso.*"}},
			Decision: "allow",
			Reason:   "allowed for tenant-a",
		}},
	}
}

// PolicyCheckRequest has no OrgId field; the tenant can arrive in Tenant,
// Meta.TenantId or the transport IdentityBinding, so each variant sets only
// one of them and the requests differ in nothing else.
func TestTenantIsolationDecisionCache(t *testing.T) {
	variants := []struct {
		name string
		req  func(tenant string) *pb.PolicyCheckRequest
	}{
		{"Tenant", func(tenant string) *pb.PolicyCheckRequest {
			return &pb.PolicyCheckRequest{JobId: "job-iso", Topic: "job.iso.run", Tenant: tenant}
		}},
		{"MetaTenantId", func(tenant string) *pb.PolicyCheckRequest {
			return &pb.PolicyCheckRequest{JobId: "job-iso", Topic: "job.iso.run", Meta: &pb.JobMetadata{TenantId: tenant}}
		}},
		{"IdentityTenantId", func(tenant string) *pb.PolicyCheckRequest {
			return &pb.PolicyCheckRequest{JobId: "job-iso", Topic: "job.iso.run", Identity: &pb.IdentityBinding{
				TenantId: tenant, PrincipalId: "principal-iso", ActorId: "actor-iso",
			}}
		}},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			srv := &server{cacheTTL: time.Minute, cache: map[string]cacheEntry{}, cacheMaxSize: 100}
			if err := srv.setPolicy(context.Background(), isoTenantAllowPolicy(), "snap-iso"); err != nil {
				t.Fatalf("setPolicy: %v", err)
			}
			first := isoEvaluate(t, srv, v.req("tenant-a"))
			if first.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW || first.GetRuleId() != "iso-allow-tenant-a" {
				t.Fatalf("tenant-a first decision = %v rule %q, want ALLOW by iso-allow-tenant-a", first.GetDecision(), first.GetRuleId())
			}
			markOnlyCachedDecision(t, srv)
			if hit := isoEvaluate(t, srv, v.req("tenant-a")); hit.GetReason() != isoCachedMarker {
				t.Fatalf("tenant-a repeat reason = %q, want the marked cache entry", hit.GetReason())
			}

			b := isoEvaluate(t, srv, v.req("tenant-b"))
			if b.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY || b.GetRuleId() == "iso-allow-tenant-a" || b.GetReason() == isoCachedMarker {
				t.Fatalf("tenant-b got decision %v rule %q reason %q, want its own default DENY", b.GetDecision(), b.GetRuleId(), b.GetReason())
			}
			if got := cacheSize(srv); got != 2 {
				t.Fatalf("decision cache entries = %d, want tenant-a's and tenant-b's", got)
			}
			if again := isoEvaluate(t, srv, v.req("tenant-a")); again.GetReason() != isoCachedMarker || again.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
				t.Fatalf("tenant-a entry changed after tenant-b's call: %v %q", again.GetDecision(), again.GetReason())
			}
		})
	}
}

// TestTenantIsolationAgentCache: tenant-b names tenant-a's agent id. The
// policy allows only low-risk agents, so enrichment from tenant-a's record
// would turn tenant-b's default DENY into an ALLOW.
func TestTenantIsolationAgentCache(t *testing.T) {
	srv, mr := newTestRedisServer(t)
	srv.agentStore = store.NewAgentIdentityStoreFromClient(srv.resultClient)
	srv.agentCacheTTL = defaultAgentCacheTTL
	ctx := context.Background()
	if _, err := srv.agentStore.Create(ctx, store.AgentIdentity{
		ID: "agent-iso", TenantID: "tenant-a", Name: "agent-a-name", Owner: "owner-a", RiskTier: "low",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := srv.setPolicy(ctx, &config.SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{{
			ID:       "iso-low-risk-agent",
			Match:    config.PolicyMatch{Topics: []string{"job.iso.*"}, AgentRiskTiers: []string{"low"}},
			Decision: "allow",
		}},
	}, "snap-iso-agent"); err != nil {
		t.Fatalf("setPolicy: %v", err)
	}
	req := func(tenant string) *pb.PolicyCheckRequest {
		return &pb.PolicyCheckRequest{JobId: "job-iso", Topic: "job.iso.run", Tenant: tenant, Labels: map[string]string{"agent_id": "agent-iso"}}
	}

	if a := isoEvaluate(t, srv, req("tenant-a")); a.GetRuleId() != "iso-low-risk-agent" {
		t.Fatalf("tenant-a decision %v rule %q, want ALLOW from its enriched agent", a.GetDecision(), a.GetRuleId())
	}
	if cached := srv.getAgentFromCache(agentCacheKey("tenant-a", "agent-iso")); cached == nil || cached.TenantID != "tenant-a" {
		t.Fatalf("agent cache entry for tenant-a = %+v, want tenant-a's record", cached)
	}
	commands := mr.CommandCount()
	if a := isoEvaluate(t, srv, req("tenant-a")); a.GetRuleId() != "iso-low-risk-agent" || mr.CommandCount() != commands {
		t.Fatalf("tenant-a repeat rule %q with %d store commands, want a cache hit with none", a.GetRuleId(), mr.CommandCount()-commands)
	}

	b := isoEvaluate(t, srv, req("tenant-b"))
	if b.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY || b.GetRuleId() == "iso-low-risk-agent" {
		t.Fatalf("tenant-b got decision %v rule %q, want DENY with no enrichment", b.GetDecision(), b.GetRuleId())
	}
	if leaked := srv.getAgentFromCache(agentCacheKey("tenant-b", "agent-iso")); leaked != nil {
		t.Fatalf("tenant-b agent cache entry = %+v, want none", leaked)
	}
	stored, err := srv.agentStore.Get(ctx, "tenant-a", "agent-iso")
	if err != nil || stored == nil || stored.RiskTier != "low" || stored.Name != "agent-a-name" {
		t.Fatalf("tenant-a agent after tenant-b's call = %+v, %v; want unchanged", stored, err)
	}
}

// TestTenantIsolationVelocityCounter: a rule keyed by the topic alone counts
// each tenant's requests in that tenant's own bucket.
func TestTenantIsolationVelocityCounter(t *testing.T) {
	srv, mr := newTestServerWithVelocity(t, &config.SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{
			{
				ID:       "iso-velocity",
				Match:    config.PolicyMatch{Topics: []string{"job.iso.*"}},
				Velocity: &config.VelocityConfig{MaxRequests: 2, WindowSeconds: 60, Key: "topic"},
				Decision: "deny",
				Reason:   "velocity limit exceeded",
			},
			{ID: "iso-allow", Match: config.PolicyMatch{Topics: []string{"job.iso.*"}}, Decision: "allow"},
		},
	}, "snap-iso-velocity")
	req := func(tenant, job string) *pb.PolicyCheckRequest {
		return &pb.PolicyCheckRequest{JobId: job, Topic: "job.iso.run", Tenant: tenant}
	}

	for i, want := range []string{"iso-allow", "iso-allow", "iso-velocity"} {
		if got := isoEvaluate(t, srv, req("tenant-a", fmt.Sprintf("job-a-%d", i+1))).GetRuleId(); got != want {
			t.Fatalf("tenant-a request %d rule %q, want %q", i+1, got, want)
		}
	}
	members, err := mr.ZMembers(velocityKey("iso-velocity", "tenant-a", "job.iso.run"))
	if err != nil || len(members) != 3 {
		t.Fatalf("tenant-a velocity members = %v, %v; want its 3 requests", members, err)
	}

	b := isoEvaluate(t, srv, req("tenant-b", "job-b-1"))
	if b.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW || b.GetRuleId() != "iso-allow" {
		t.Fatalf("tenant-b first request got decision %v rule %q reason %q, want ALLOW within its own count", b.GetDecision(), b.GetRuleId(), b.GetReason())
	}
}

func isoEvaluate(t *testing.T, srv *server, req *pb.PolicyCheckRequest) *pb.PolicyCheckResponse {
	t.Helper()
	resp, err := srv.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return resp
}

// markOnlyCachedDecision rewrites the reason of the single cached decision, so
// a later response carrying the marker can only have come from the cache.
func markOnlyCachedDecision(t *testing.T, srv *server) {
	t.Helper()
	srv.cacheMu.Lock()
	defer srv.cacheMu.Unlock()
	if len(srv.cache) != 1 {
		t.Fatalf("decision cache entries = %d, want only tenant-a's", len(srv.cache))
	}
	for _, entry := range srv.cache {
		entry.resp.Reason = isoCachedMarker
	}
}
