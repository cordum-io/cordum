package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
)

// TestResolveMCPIdentity_CrossTenantHeaderReturnsNil locks the HIGH
// cross-tenant finding: resolveMCPIdentity looked up the agent identity
// with an empty tenant (s.agentIdentityStore.Get(ctx, "", id)), which hits
// the store's documented system-lookup bypass and skips the tenant check.
// A caller authenticated to tenant-b could therefore assume tenant-a's
// agent identity — and its AllowedTools / AllowedServers / RiskTier — by
// passing the (non-secret) X-Agent-Id of a tenant-a agent. The fix scopes
// the lookup to tenantFromRequest(r); a mismatching tenant must resolve to
// nil (fail-closed: zero tools visible).
func TestResolveMCPIdentity_CrossTenantHeaderReturnsNil(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if _, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		ID:           "agent-secret",
		TenantID:     "tenant-a",
		Name:         "Tenant-A Secret Agent",
		Owner:        "alice@example.com",
		RiskTier:     "high",
		AllowedTools: []string{"private_tool"},
		Status:       "active",
	}); err != nil {
		t.Fatalf("Create agent identity: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	// Caller is in tenant-b but points X-Agent-Id at tenant-a's agent.
	req.Header.Set("X-Tenant-ID", "tenant-b")
	req.Header.Set(mcpAgentIDHeader, "agent-secret")

	if got := s.resolveMCPIdentity(req); got != nil {
		t.Fatalf("resolveMCPIdentity across tenants = %+v, want nil (cross-tenant identity must not resolve)", got)
	}
}

// TestResolveMCPIdentity_SameTenantReturnsIdentity is the positive control:
// a caller in the agent's own tenant still resolves the identity with its
// AllowedTools — the fix must not regress legitimate same-tenant access.
func TestResolveMCPIdentity_SameTenantReturnsIdentity(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if _, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		ID:           "agent-ok",
		TenantID:     "tenant-a",
		Name:         "Tenant-A Agent",
		Owner:        "alice@example.com",
		RiskTier:     "low",
		AllowedTools: []string{"safe_tool"},
		Status:       "active",
	}); err != nil {
		t.Fatalf("Create agent identity: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set(mcpAgentIDHeader, "agent-ok")

	got := s.resolveMCPIdentity(req)
	if got == nil {
		t.Fatalf("resolveMCPIdentity same-tenant = nil, want identity with AllowedTools")
	}
	if len(got.AllowedTools) != 1 || got.AllowedTools[0] != "safe_tool" {
		t.Fatalf("AllowedTools = %v, want [safe_tool]", got.AllowedTools)
	}
}

// TestResolveMCPIdentity_NoTenantReturnsNil confirms the fail-closed guard:
// an X-Agent-Id with no resolvable tenant must not fall back to the empty-
// tenant store bypass.
func TestResolveMCPIdentity_NoTenantReturnsNil(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if _, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		ID:           "agent-secret",
		TenantID:     "tenant-a",
		Name:         "Tenant-A Secret Agent",
		Owner:        "alice@example.com",
		RiskTier:     "high",
		AllowedTools: []string{"private_tool"},
		Status:       "active",
	}); err != nil {
		t.Fatalf("Create agent identity: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	// No X-Tenant-ID, no auth context tenant.
	req.Header.Set(mcpAgentIDHeader, "agent-secret")

	if got := s.resolveMCPIdentity(req); got != nil {
		t.Fatalf("resolveMCPIdentity with no tenant = %+v, want nil (fail-closed)", got)
	}
}

// TestResolveMCPIdentity_WorkerIDPathRejectsCrossTenant covers the SECOND
// resolution path (no X-Agent-Id header → GetByWorkerID by authenticated
// principal). GetByWorkerID looks the agent up with an empty tenant, so the
// resolved identity's tenant must be checked against the request tenant —
// a worker credential bound to another tenant's agent must not apply that
// agent's scope.
func TestResolveMCPIdentity_WorkerIDPathRejectsCrossTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	created, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		ID:           "agent-wkr-a",
		TenantID:     "tenant-a",
		Name:         "Tenant-A Worker Agent",
		Owner:        "alice@example.com",
		RiskTier:     "high",
		AllowedTools: []string{"private_tool"},
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.agentIdentityStore.LinkWorker(ctx, created.ID, "worker-x"); err != nil {
		t.Fatalf("LinkWorker: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-Tenant-ID", "tenant-b") // cross-tenant request
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{
		PrincipalID: "worker-x",
		Tenant:      "tenant-b",
	}))

	if got := s.resolveMCPIdentity(req); got != nil {
		t.Fatalf("worker-ID path resolved a cross-tenant identity: %+v", got)
	}
}

// TestResolveMCPIdentity_WorkerIDPathSameTenant is the positive control for
// the worker-ID path: a same-tenant worker still resolves its identity.
func TestResolveMCPIdentity_WorkerIDPathSameTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	created, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		ID:           "agent-wkr-ok",
		TenantID:     "tenant-a",
		Name:         "Tenant-A Worker Agent",
		Owner:        "alice@example.com",
		RiskTier:     "low",
		AllowedTools: []string{"safe_tool"},
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.agentIdentityStore.LinkWorker(ctx, created.ID, "worker-y"); err != nil {
		t.Fatalf("LinkWorker: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{
		PrincipalID: "worker-y",
		Tenant:      "tenant-a",
	}))

	got := s.resolveMCPIdentity(req)
	if got == nil {
		t.Fatal("same-tenant worker-ID path = nil, want identity")
	}
	if len(got.AllowedTools) != 1 || got.AllowedTools[0] != "safe_tool" {
		t.Fatalf("AllowedTools = %v, want [safe_tool]", got.AllowedTools)
	}
}
