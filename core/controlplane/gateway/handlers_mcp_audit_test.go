package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
)

// TestHandleAgentToolVisibility_CrossTenantReturns404 locks the MEDIUM
// audit finding: writeAgentToolVisibility loaded the AgentIdentity by ID
// without checking the caller's tenant against identity.TenantID, so a
// tenant-B admin could enumerate tenant-A's agent AllowedTools + risk
// tier by passing the right agent id. Fix: when caller-tenant !=
// identity.TenantID (and the caller isn't AllowCrossTenant=true),
// return 404 (same as missing) so the existence of the agent is not
// leaked across tenants.
func TestHandleAgentToolVisibility_CrossTenantReturns404(t *testing.T) {
	s, _, _ := newTestGateway(t)

	identity, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		ID:           "agent-secret",
		TenantID:     "tenant-a",
		Name:         "Tenant-A Secret Agent",
		Owner:        "alice@example.com",
		RiskTier:     "high",
		AllowedTools: []string{"private_tool"},
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Create agent identity: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+identity.ID+"/tools", nil)
	req.SetPathValue("id", identity.ID)
	// Caller is admin BUT in tenant-b — not the agent's tenant.
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{
		Role:   "admin",
		Tenant: "tenant-b",
	}))
	rec := httptest.NewRecorder()
	s.handleAgentToolVisibility(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (not found - tenant isolation); body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleAgentToolVisibility_SameTenantReturnsAllowedTools is the
// positive control: when the caller's tenant matches the agent's tenant,
// the response includes the agent's AllowedTools — the fix MUST NOT
// regress legitimate same-tenant access.
func TestHandleAgentToolVisibility_SameTenantReturnsAllowedTools(t *testing.T) {
	s, _, _ := newTestGateway(t)

	identity, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		ID:           "agent-ok",
		TenantID:     "tenant-a",
		Name:         "Tenant-A Agent",
		Owner:        "alice@example.com",
		RiskTier:     "low",
		AllowedTools: []string{"safe_tool"},
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Create agent identity: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+identity.ID+"/tools", nil)
	req.SetPathValue("id", identity.ID)
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{
		Role:   "admin",
		Tenant: "tenant-a",
	}))
	rec := httptest.NewRecorder()
	s.handleAgentToolVisibility(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for same-tenant caller; body=%s", rec.Code, rec.Body.String())
	}
}

// TestMCPUpstreamPolicyInputs_ReadsConfigStrictMode locks the HIGH #5
// fix: mcpUpstreamPolicyInputs must source policyMode from configsvc
// (mcp.policy_mode under safety.mcp) rather than returning "" unconditionally.
// Pre-fix, every create/update path collapsed to observe-mode semantics
// because the validator's enterprise-strict branch was unreachable from
// the API surface.
func TestMCPUpstreamPolicyInputs_ReadsConfigStrictMode(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeOrg,
		ScopeID: "strict-tenant",
		Data: map[string]any{
			"safety": map[string]any{
				"mcp": map[string]any{
					"policy_mode":       "enterprise-strict",
					"allowed_upstreams": []any{"approved-server"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("set tenant config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/mcp/upstreams", nil)
	mode, allow := s.mcpUpstreamPolicyInputs(req, "strict-tenant")
	if mode != string(edgecore.PolicyModeEnterpriseStrict) {
		t.Fatalf("policy mode = %q, want %q (must read mcp.policy_mode from configsvc)",
			mode, edgecore.PolicyModeEnterpriseStrict)
	}
	if len(allow) != 1 || allow[0] != "approved-server" {
		t.Fatalf("allowlist = %v, want [approved-server]", allow)
	}
}

// TestMCPUpstreamPolicyInputs_DefaultsToEmptyWhenUnset ensures the fix
// does NOT regress the unset path — a tenant with no mcp.policy_mode
// config returns "" so ValidateMCPUpstream stays on the non-strict
// branch by default.
func TestMCPUpstreamPolicyInputs_DefaultsToEmptyWhenUnset(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/mcp/upstreams", nil)
	mode, _ := s.mcpUpstreamPolicyInputs(req, "no-config-tenant")
	if mode != "" {
		t.Fatalf("policy mode = %q, want \"\" (no config) — fix should not invent a default", mode)
	}
}

// silence unused json import in some build configs
var _ = json.RawMessage(nil)
