package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
)

// newMCPIdentityRequest builds an MCP request for tenant, authenticated as
// principal when principal is non-empty.
func newMCPIdentityRequest(tenant, principal string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if principal != "" {
		req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{
			PrincipalID: principal,
			Tenant:      tenant,
		}))
	}
	return req
}

// assertNoMCPIdentity fails unless resolveMCPIdentity returned ("", nil).
func assertNoMCPIdentity(t *testing.T, agentID string, identity *mcp.AgentIdentity) {
	t.Helper()
	if agentID != "" || identity != nil {
		t.Fatalf("resolveMCPIdentity = (%q, %+v), want (\"\", nil)", agentID, identity)
	}
}

// TestResolveMCPIdentity_CrossTenantHeaderReturnsNil locks the HIGH
// cross-tenant finding: a caller authenticated to tenant-b must not assume
// tenant-a's agent identity — and its AllowedTools / AllowedServers /
// RiskTier — by passing the (non-secret) X-Agent-Id of a tenant-a agent.
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

	// Caller is in tenant-b but points X-Agent-Id at tenant-a's agent.
	req := newMCPIdentityRequest("tenant-b", "")
	req.Header.Set(mcpAgentIDHeader, "agent-secret")

	agentID, identity := s.resolveMCPIdentity(req)
	assertNoMCPIdentity(t, agentID, identity)
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

	// No X-Tenant-ID, no auth context tenant.
	req := newMCPIdentityRequest("", "")
	req.Header.Set(mcpAgentIDHeader, "agent-secret")

	agentID, identity := s.resolveMCPIdentity(req)
	assertNoMCPIdentity(t, agentID, identity)
}

// TestResolveMCPIdentity_WorkerIDPathRejectsCrossTenant: GetByWorkerID
// looks the agent up with an empty tenant, so the resolved identity's
// tenant must be checked against the request tenant — a worker credential
// bound to another tenant's agent must not apply that agent's scope.
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

	agentID, identity := s.resolveMCPIdentity(newMCPIdentityRequest("tenant-b", "worker-x"))
	assertNoMCPIdentity(t, agentID, identity)
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

	agentID, identity := s.resolveMCPIdentity(newMCPIdentityRequest("tenant-a", "worker-y"))
	if agentID != created.ID || identity == nil || identity.ID != created.ID {
		t.Fatalf("same-tenant worker-ID path = (%q, %+v), want %s", agentID, identity, created.ID)
	}
	if len(identity.AllowedTools) != 1 || identity.AllowedTools[0] != "safe_tool" {
		t.Fatalf("AllowedTools = %v, want [safe_tool]", identity.AllowedTools)
	}
}

// TestResolveMCPIdentity_HeaderRule locks ruling Q4: X-Agent-Id may confirm
// the identity bound to the authenticated principal, never select another
// same-tenant identity. worker-z is bound to agent-bound; worker-u is not.
func TestResolveMCPIdentity_HeaderRule(t *testing.T) {
	cases := []struct {
		name, principal, header, status, wantAgent string
		wantTools                                  []string
	}{
		{name: "bound no header", principal: "worker-z", wantAgent: "agent-bound", wantTools: []string{"safe_tool"}},
		{name: "bound matching header", principal: "worker-z", header: "agent-bound", wantAgent: "agent-bound", wantTools: []string{"safe_tool"}},
		{name: "bound padded header", principal: "worker-z", header: " agent-bound ", wantAgent: "agent-bound", wantTools: []string{"safe_tool"}},
		{name: "bound whitespace-only header", principal: "worker-z", header: "   ", wantAgent: "agent-bound", wantTools: []string{"safe_tool"}},
		{name: "bound mismatched header", principal: "worker-z", header: "agent-other"},
		{name: "bound case-mismatched header", principal: "worker-z", header: "AGENT-BOUND"},
		{name: "unbound with header", principal: "worker-u", header: "agent-other"},
		{name: "unbound no header", principal: "worker-u"},
		{name: "bound but suspended", principal: "worker-z", status: "suspended", wantAgent: "agent-bound"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			ctx := context.Background()
			boundStatus := tc.status
			if boundStatus == "" {
				boundStatus = "active"
			}
			for _, a := range []store.AgentIdentity{
				{ID: "agent-bound", AllowedTools: []string{"safe_tool"}, Status: boundStatus},
				{ID: "agent-other", AllowedTools: []string{"other_tool"}, Status: "active"},
			} {
				a.TenantID, a.Name, a.Owner, a.RiskTier = "tenant-a", a.ID, "alice@example.com", "high"
				if _, err := s.agentIdentityStore.Create(ctx, a); err != nil {
					t.Fatalf("Create %s: %v", a.ID, err)
				}
			}
			if err := s.agentIdentityStore.LinkWorker(ctx, "agent-bound", "worker-z"); err != nil {
				t.Fatalf("LinkWorker: %v", err)
			}
			req := newMCPIdentityRequest("tenant-a", tc.principal)
			if tc.header != "" {
				req.Header.Set(mcpAgentIDHeader, tc.header)
			}

			agentID, identity := s.resolveMCPIdentity(req)
			if agentID != tc.wantAgent {
				t.Fatalf("agentID = %q, want %q", agentID, tc.wantAgent)
			}
			if tc.wantTools == nil {
				if identity != nil {
					t.Fatalf("identity = %+v, want nil", identity)
				}
				return
			}
			if identity == nil || identity.ID != tc.wantAgent || len(identity.AllowedTools) != 1 || identity.AllowedTools[0] != tc.wantTools[0] {
				t.Fatalf("identity = %+v, want %s with %v", identity, tc.wantAgent, tc.wantTools)
			}
		})
	}
}

// seedMCPBindingAgent creates an active same-tenant agent identity whose
// scope admits the tools the binding tests call (untiered tools such as
// cordum_status count as HIGH, so the identity must be "high").
func seedMCPBindingAgent(t *testing.T, s *server, tenant, id string) {
	t.Helper()
	if _, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		ID:           id,
		TenantID:     tenant,
		Name:         id,
		Owner:        "alice@example.com",
		RiskTier:     "high",
		AllowedTools: []string{mcp.ToolStatus, mcp.ToolCreateWorkflow, "safe_tool"},
		Status:       "active",
	}); err != nil {
		t.Fatalf("Create agent identity %s: %v", id, err)
	}
}

type mcpBindingRPCResponse struct {
	Result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	} `json:"result"`
	Error *struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	} `json:"error"`
}

// errorData decodes error.data when it is an object (-32098 / -32099);
// other codes may carry a plain string, which yields zero values.
func (r mcpBindingRPCResponse) errorData() (subReason, approvalID string) {
	var data struct {
		SubReason  string `json:"sub_reason"`
		ApprovalID string `json:"approval_id"`
	}
	if r.Error != nil {
		_ = json.Unmarshal(r.Error.Data, &data)
	}
	return data.SubReason, data.ApprovalID
}

// pendingMCPApprovals lists every pending MCP approval for tool.
func pendingMCPApprovals(t *testing.T, approvals *MCPApprovalStore, tool string) []*MCPApprovalRecord {
	t.Helper()
	recs, _, err := approvals.ListByStatus(context.Background(), string(model.ApprovalStatusPending), 0, 100)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	var out []*MCPApprovalRecord
	for _, rec := range recs {
		if rec.ToolName == tool {
			out = append(out, rec)
		}
	}
	return out
}

// postMCPBindingRPC sends one JSON-RPC request to /mcp/message as the
// mcpTestAuth principal "tester", optionally with an X-Agent-Id header.
func postMCPBindingRPC(t *testing.T, baseURL, agentHeader, method, params string) (int, mcpBindingRPCResponse) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/message", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("X-Tenant-ID", "default")
	if agentHeader != "" {
		req.Header.Set(mcpAgentIDHeader, agentHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s request: %v", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out mcpBindingRPCResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s response: %v body=%s", method, err, raw)
		}
	}
	return resp.StatusCode, out
}

// newMCPBindingServer serves the HTTP MCP routes for tenant "default" as
// principal "tester", which is bound to agent-bound; agent-other is an
// active, unbound identity in the same tenant with the same scope.
func newMCPBindingServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	ctx := context.Background()
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope: configsvc.ScopeSystem, ScopeID: "default",
		Data: map[string]any{"mcp": map[string]any{"enabled": true, "transport": "http"}},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}
	seedMCPBindingAgent(t, s, "default", "agent-bound")
	seedMCPBindingAgent(t, s, "default", "agent-other")
	if err := s.agentIdentityStore.LinkWorker(ctx, "agent-bound", "tester"); err != nil {
		t.Fatalf("LinkWorker: %v", err)
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)
	return s, httpSrv
}

// TestMCPIdentityBinding_EndToEnd drives the HTTP MCP transport (ruling Q4):
// X-Agent-Id may confirm the identity bound to the authenticated principal,
// never select another same-tenant identity, and approvals record the bound
// ID rather than the raw header.
func TestMCPIdentityBinding_EndToEnd(t *testing.T) {
	s, httpSrv := newMCPBindingServer(t)
	approvals := s.getMCPRuntime().approvalStore
	statusCall := `{"name":"` + mcp.ToolStatus + `","arguments":{}}`
	createCall := `{"name":"` + mcp.ToolCreateWorkflow + `","arguments":{"name":"bind-check"}}`

	t.Run("mismatched header lists no tools", func(t *testing.T) {
		code, resp := postMCPBindingRPC(t, httpSrv.URL, "agent-other", "tools/list", `{}`)
		if code != http.StatusOK || len(resp.Result.Tools) != 0 {
			t.Fatalf("tools/list as agent-other: status=%d tools=%v, want 200 and 0 tools", code, resp.Result.Tools)
		}
	})
	t.Run("mismatched header call is denied", func(t *testing.T) {
		code, resp := postMCPBindingRPC(t, httpSrv.URL, "agent-other", "tools/call", statusCall)
		subReason, _ := resp.errorData()
		if code != http.StatusOK || resp.Error == nil || resp.Error.Code != -32098 || subReason != "no_identity" {
			t.Fatalf("tools/call as agent-other: status=%d error=%+v sub_reason=%q, want -32098 no_identity", code, resp.Error, subReason)
		}
	})
	t.Run("mismatched header enqueues no approval", func(t *testing.T) {
		code, resp := postMCPBindingRPC(t, httpSrv.URL, "agent-other", "tools/call", createCall)
		for _, rec := range pendingMCPApprovals(t, approvals, mcp.ToolCreateWorkflow) {
			t.Fatalf("approval %s was enqueued for agent %q from a mismatched header", rec.ID, rec.AgentID)
		}
		if code != http.StatusOK || resp.Error == nil || resp.Error.Code != -32098 {
			t.Fatalf("create_workflow as agent-other: status=%d error=%+v, want -32098", code, resp.Error)
		}
	})
	for _, header := range []string{"", "agent-bound"} {
		t.Run("bound identity lists tools header="+header, func(t *testing.T) {
			code, resp := postMCPBindingRPC(t, httpSrv.URL, header, "tools/list", `{}`)
			found := false
			for _, tool := range resp.Result.Tools {
				found = found || tool.Name == mcp.ToolStatus
			}
			if code != http.StatusOK || !found {
				t.Fatalf("tools/list header=%q: status=%d tools=%v, want %s", header, code, resp.Result.Tools, mcp.ToolStatus)
			}
		})
	}
	// The stored record is the assertion target: the legacy -32099 encoding
	// is broken upstream (task-ff988dfa: UnixMicro ExpiresAt decoded as
	// seconds makes the response 500), so approval_id is cross-checked only
	// when the response carries it.
	t.Run("approval records the bound id", func(t *testing.T) {
		before := map[string]bool{}
		for _, rec := range pendingMCPApprovals(t, approvals, mcp.ToolCreateWorkflow) {
			before[rec.ID] = true
		}
		code, resp := postMCPBindingRPC(t, httpSrv.URL, "", "tools/call", createCall)
		var recs []*MCPApprovalRecord
		for _, rec := range pendingMCPApprovals(t, approvals, mcp.ToolCreateWorkflow) {
			if !before[rec.ID] {
				recs = append(recs, rec)
			}
		}
		if len(recs) != 1 {
			t.Fatalf("new pending create_workflow approvals = %d (status=%d error=%+v), want 1", len(recs), code, resp.Error)
		}
		if rec := recs[0]; rec.AgentID != "agent-bound" || rec.Principal != "tester" {
			t.Fatalf("stored approval AgentID=%q Principal=%q, want agent-bound/tester", rec.AgentID, rec.Principal)
		}
		if _, approvalID := resp.errorData(); approvalID != "" && approvalID != recs[0].ID {
			t.Fatalf("response approval_id = %q, want stored %q", approvalID, recs[0].ID)
		}
	})
}

// TestMCPAuthRecordsBoundAgentIDNeverRawHeader asserts every metadata sink
// mcpAuth fills carries the principal-bound agent ID (or nothing), never the
// client-chosen X-Agent-Id.
func TestMCPAuthRecordsBoundAgentIDNeverRawHeader(t *testing.T) {
	cases := []struct {
		name, header, wantAgent string
		bound                   bool
	}{
		{name: "bound no header", bound: true, wantAgent: "agent-bound"},
		{name: "bound mismatched header", bound: true, header: "agent-other"},
		{name: "unbound with header", header: "agent-other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
			seedMCPBindingAgent(t, s, "default", "agent-bound")
			seedMCPBindingAgent(t, s, "default", "agent-other")
			if tc.bound {
				if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-bound", "tester"); err != nil {
					t.Fatalf("LinkWorker: %v", err)
				}
			}
			var gateMeta MCPCallMetadata
			var callMeta mcp.CallMetadata
			var identity *mcp.AgentIdentity
			handler := s.mcpAuth(func(w http.ResponseWriter, r *http.Request) {
				gateMeta, _ = MCPCallMetadataFromContext(r.Context())
				callMeta, _ = mcp.CallMetadataFromContext(r.Context())
				identity = mcp.IdentityFromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/mcp/message", nil)
			req.Header.Set("X-API-Key", "test-key")
			req.Header.Set("X-Tenant-ID", "default")
			req.Header.Set("X-MCP-Session-ID", "sess-bind")
			if tc.header != "" {
				req.Header.Set(mcpAgentIDHeader, tc.header)
			}
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
			}
			if gateMeta.AgentID != tc.wantAgent || callMeta.AgentID != tc.wantAgent {
				t.Fatalf("AgentID gate=%q call=%q, want %q", gateMeta.AgentID, callMeta.AgentID, tc.wantAgent)
			}
			if gateMeta.Principal != "tester" || callMeta.Principal != "tester" {
				t.Fatalf("Principal gate=%q call=%q, want tester", gateMeta.Principal, callMeta.Principal)
			}
			gotID := ""
			if identity != nil {
				gotID = identity.ID
			}
			if gotID != tc.wantAgent {
				t.Fatalf("identity = %q, want %q", gotID, tc.wantAgent)
			}
		})
	}
}
