package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
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

// newMCPBindingGateway serves mcpAuth behind static keys for viewers alice
// and bob and admin root (tenant default). Agent identity agent-a is linked to
// alice's principal, agent-b to bob's API key id, and agent-x (tenant other)
// to bob's principal.
func newMCPBindingGateway(t *testing.T) *server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"key-alice","tenant":"default","principal_id":"alice","role":"viewer"},` +
			`{"key":"key-bob","tenant":"default","principal_id":"bob","role":"viewer"},` +
			`{"key":"key-root","tenant":"default","principal_id":"root","role":"admin"}]`,
	})
	ctx := context.Background()
	for _, link := range []struct{ id, tenant, worker string }{
		{"agent-a", "default", "alice"},
		{"agent-b", "default", "static:" + auth.APIKeyFingerprint("key-bob")},
		{"agent-x", "other", "bob"},
	} {
		if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
			ID: link.id, TenantID: link.tenant, Name: link.id, Owner: "ops", RiskTier: "low",
			AllowedTools: []string{"safe_tool"}, Status: "active",
		}); err != nil {
			t.Fatalf("Create %s: %v", link.id, err)
		}
		if err := s.agentIdentityStore.LinkWorker(ctx, link.id, link.worker); err != nil {
			t.Fatalf("LinkWorker %s: %v", link.id, err)
		}
	}
	return s
}

// mcpAuthAs runs one POST through mcpAuth as the given API key, optionally
// naming an agent in X-Agent-Id, and reports what the wrapped handler saw.
func mcpAuthAs(t *testing.T, s *server, key, agentID string) (code int, body, identity, metaAgent string) {
	t.Helper()
	h := s.mcpAuth(func(w http.ResponseWriter, r *http.Request) {
		if id := mcp.IdentityFromContext(r.Context()); id != nil {
			identity = id.ID
		}
		if meta, ok := MCPCallMetadataFromContext(r.Context()); ok {
			metaAgent = meta.AgentID
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/message", nil)
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", "default")
	if agentID != "" {
		req.Header.Set(mcpAgentIDHeader, agentID)
	}
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr.Code, rr.Body.String(), identity, metaAgent
}

// X-Agent-Id is a claim: a tenant user may act only as an identity linked to
// its own principal or API key, unless it may manage agent identities anyway.
func TestMCPAuthAgentIDHeaderRequiresBinding(t *testing.T) {
	s := newMCPBindingGateway(t)
	for _, tc := range []struct {
		name, key, header string
		wantCode          int
		wantIdentity      string
		wantMetaAgent     string
	}{
		{"user cannot adopt another user's agent", "key-bob", "agent-a", http.StatusForbidden, "", ""},
		{"unknown agent looks the same as unbound", "key-bob", "agent-missing", http.StatusForbidden, "", ""},
		{"link in another tenant does not bind", "key-bob", "agent-x", http.StatusForbidden, "", ""},
		{"bound by principal", "key-alice", "agent-a", http.StatusNoContent, "agent-a", "agent-a"},
		{"bound by API key id", "key-bob", "agent-b", http.StatusNoContent, "agent-b", "agent-b"},
		{"admin may act as any agent", "key-root", "agent-a", http.StatusNoContent, "agent-a", "agent-a"},
		{"no header keeps the linked identity", "key-alice", "", http.StatusNoContent, "agent-a", "alice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body, identity, metaAgent := mcpAuthAs(t, s, tc.key, tc.header)
			if code != tc.wantCode || identity != tc.wantIdentity || metaAgent != tc.wantMetaAgent {
				t.Fatalf("got code=%d identity=%q meta.AgentID=%q body=%s; want code=%d identity=%q meta.AgentID=%q",
					code, identity, metaAgent, body, tc.wantCode, tc.wantIdentity, tc.wantMetaAgent)
			}
			if code == http.StatusForbidden && !strings.Contains(body, "agent identity not bound to caller") {
				t.Fatalf("403 body = %s, want the binding error", body)
			}
		})
	}
}

// The legacy switch restores header trust for unbound identities, still
// tenant-scoped.
func TestMCPAuthAllowUnboundAgentIDSwitch(t *testing.T) {
	s := newMCPBindingGateway(t)
	t.Setenv(mcpAllowUnboundAgentIDEnv, "true")
	if code, body, identity, _ := mcpAuthAs(t, s, "key-bob", "agent-a"); code != http.StatusNoContent || identity != "agent-a" {
		t.Fatalf("legacy switch: code=%d identity=%q body=%s, want 204 as agent-a", code, identity, body)
	}
	if code, _, identity, _ := mcpAuthAs(t, s, "key-bob", "agent-x"); code != http.StatusNoContent || identity != "" {
		t.Fatalf("legacy switch, other tenant's agent: code=%d identity=%q, want 204 with no identity", code, identity)
	}
}
