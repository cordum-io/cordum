package agentd

import (
	"context"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

// isoTenantGateway allows tenant-a's action and denies every other tenant's,
// so a tenant-b hook answered from tenant-a's cached ALLOW would run.
type isoTenantGateway struct{ tenants []string }

func (g *isoTenantGateway) Evaluate(_ context.Context, req EvaluateRequest) (*EvaluateResponse, error) {
	g.tenants = append(g.tenants, req.TenantID)
	resp := &EvaluateResponse{
		Decision: string(edgecore.DecisionDeny), PermissionDecision: "deny", Reason: "denied for " + req.TenantID,
		PolicySnapshot: "snap-eval", InputHash: req.InputHash, CacheEligible: true,
	}
	if req.TenantID == "tenant-a" {
		resp.Decision, resp.PermissionDecision, resp.Reason = string(edgecore.DecisionAllow), "allow", "allowed for tenant-a"
	}
	return resp, nil
}

func TestTenantIsolationAgentdSafeAllowCache(t *testing.T) {
	gateway := &isoTenantGateway{}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client:      gateway,
		EventWriter: &captureEventWriter{},
		State:       evaluatorTestState(edgecore.PolicyModeEnforce),
		Cache: NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 8},
			fixedClock{now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}),
		HookTimeout: time.Second,
	})
	hook := func(tenant string) claude.AgentdDecision {
		req := evaluatorMetricsRequest()
		req.TenantID = tenant
		decision, err := evaluator.EvaluateHook(context.Background(), req)
		if err != nil {
			t.Fatalf("EvaluateHook(%s): %v", tenant, err)
		}
		return decision
	}

	if d := hook("tenant-a"); d.Decision != claude.DecisionAllow {
		t.Fatalf("tenant-a decision = %#v, want allow", d)
	}
	if d := hook("tenant-a"); d.Decision != claude.DecisionAllow || len(gateway.tenants) != 1 {
		t.Fatalf("tenant-a repeat = %#v after %d gateway calls, want a cached allow after 1", d, len(gateway.tenants))
	}

	b := hook("tenant-b")
	if b.Decision != claude.DecisionDeny || strings.Contains(b.Reason, "tenant-a") {
		t.Fatalf("tenant-b decision = %#v, want its own deny", b)
	}
	if len(gateway.tenants) != 2 || gateway.tenants[1] != "tenant-b" {
		t.Fatalf("gateway calls = %v, want tenant-b evaluated by the gateway", gateway.tenants)
	}
	if d := hook("tenant-a"); d.Decision != claude.DecisionAllow || len(gateway.tenants) != 2 {
		t.Fatalf("tenant-a entry after tenant-b's call = %#v with %d gateway calls, want the cached allow", d, len(gateway.tenants))
	}
}
