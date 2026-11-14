package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
)

// Real-provider fixtures distinguish credential-bound identity from attribution.
const mcpAssertedUnboundKey = "mcpasserted-unbound-admin-key-0000000001"
const mcpAssertedBoundKey = "mcpasserted-bound-worker-key-00000000001"
const mcpAssertedBoundApproverKey = "mcpasserted-bound-worker-key-00000000002"
const mcpAssertedOtherApproverKey = "mcpasserted-bound-approver-key-000000001"
const mcpAssertedManagedKey = "scoped-test-secret"
const mcpAssertedWorker = "worker-z"
const mcpAssertedAgent = "agent-bound"
const mcpAssertedApprover = "approver-1"

func mcpAssertedProfiles(t *testing.T, fn func(t *testing.T, env map[string]string)) {
	t.Helper()
	t.Run("production", func(t *testing.T) {
		fn(t, map[string]string{"CORDUM_ENV": "production", "CORDUM_ALLOW_HEADER_PRINCIPAL": "true"})
	})
	t.Run("development", func(t *testing.T) {
		fn(t, map[string]string{"CORDUM_ENV": "development"})
	})
}

func mcpAssertedGateway(t *testing.T, env map[string]string, gateOn bool) (*server, *auth.BasicAuthProvider) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	if gateOn {
		configurePolicyGateBootDeps(t, s)
	}
	setMCPConfigForBoot(t, s, true, gateOn)
	seedMCPBindingAgent(t, s, "default", mcpAssertedAgent)
	if err := s.agentIdentityStore.LinkWorker(context.Background(), mcpAssertedAgent, mcpAssertedWorker); err != nil {
		t.Fatalf("LinkWorker: %v", err)
	}
	keys, err := json.Marshal([]map[string]string{
		{"key": mcpAssertedUnboundKey, "tenant": "default", "role": "admin"},
		{"key": mcpAssertedBoundKey, "tenant": "default", "role": "admin", "principal_id": mcpAssertedWorker},
		{"key": mcpAssertedBoundApproverKey, "tenant": "default", "role": "admin", "principal_id": mcpAssertedWorker},
		{"key": mcpAssertedOtherApproverKey, "tenant": "default", "role": "admin", "principal_id": mcpAssertedApprover},
	})
	if err != nil {
		t.Fatalf("marshal api keys: %v", err)
	}
	pinned := map[string]string{"CORDUM_API_KEYS": string(keys)}
	for k, v := range env {
		pinned[k] = v
	}
	provider := newBasicAuthForTest(t, pinned)
	provider.SetKeyStore(&regressionScopedKeyStore{key: &auth.ManagedKey{ID: "mcpasserted-managed", Tenant: "default"}})
	s.auth = provider
	return s, provider
}

func mcpAssertedServe(t *testing.T, s *server) *httptest.Server {
	t.Helper()
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func mcpAssertedRequest(method, path, body, apiKey, principal string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-Tenant-ID", "default")
	if principal != "" {
		req.Header.Set("X-Principal-Id", principal)
	}
	return req
}

func mcpAssertedAuth(t *testing.T, p *auth.BasicAuthProvider, req *http.Request, principal string, asserted bool) *auth.AuthContext {
	t.Helper()
	ac, err := p.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("AuthenticateHTTP: %v", err)
	}
	want := principal
	if asserted {
		want = ""
	}
	if ac.PrincipalID != principal || ac.PrincipalAsserted != asserted || ac.AuthenticatedPrincipal() != want {
		t.Fatalf("auth context principal=%q asserted=%v authenticated=%q, want %q/%v/%q",
			ac.PrincipalID, ac.PrincipalAsserted, ac.AuthenticatedPrincipal(), principal, asserted, want)
	}
	return ac
}

func mcpAssertedRPC(t *testing.T, baseURL, apiKey, principal, method, params string) (int, mcpBindingRPCResponse) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/message", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-Tenant-ID", "default")
	if principal != "" {
		req.Header.Set("X-Principal-Id", principal)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s request: %v", method, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response: %v", err)
		}
	}()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var out mcpBindingRPCResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s response: %v body=%s", method, err, raw)
		}
	}
	return resp.StatusCode, out
}

func mcpAssertedApprove(t *testing.T, s *server, p *auth.BasicAuthProvider, id, apiKey, principal string, asserted bool) *httptest.ResponseRecorder {
	t.Helper()
	req := mcpAssertedRequest(http.MethodPost, "/api/v1/mcp/approvals/"+id+"/approve", `{"reason":"mcp asserted principal check"}`, apiKey, principal)
	wantPrincipal := principal
	if !asserted {
		wantPrincipal = map[string]string{mcpAssertedBoundApproverKey: mcpAssertedWorker, mcpAssertedOtherApproverKey: mcpAssertedApprover}[apiKey]
	}
	req = withAuth(req, mcpAssertedAuth(t, p, req, wantPrincipal, asserted))
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.handleMCPApprovalApprove(rec, req)
	return rec
}

func TestMCPAssertedPrincipal_ResolvesNoIdentity(t *testing.T) {
	statusCall := `{"name":"` + mcp.ToolStatus + `","arguments":{}}`
	mcpAssertedProfiles(t, func(t *testing.T, env map[string]string) {
		for _, gateOn := range []bool{false, true} {
			t.Run(map[bool]string{false: "gate off", true: "gate on"}[gateOn], func(t *testing.T) {
				s, p := mcpAssertedGateway(t, env, gateOn)
				srv := mcpAssertedServe(t, s)
				for _, key := range []string{mcpAssertedUnboundKey, mcpAssertedManagedKey} {
					mcpAssertedAuth(t, p, mcpAssertedRequest(http.MethodPost, "/mcp/message", "", key, mcpAssertedWorker), mcpAssertedWorker, true)
					code, resp := mcpAssertedRPC(t, srv.URL, key, mcpAssertedWorker, "tools/list", `{}`)
					if code != http.StatusOK || resp.Error != nil || len(resp.Result.Tools) != 0 {
						t.Fatalf("tools/list asserted as %s: status=%d error=%+v tools=%v, want 200 and 0 tools", mcpAssertedWorker, code, resp.Error, resp.Result.Tools)
					}
					code, resp = mcpAssertedRPC(t, srv.URL, key, mcpAssertedWorker, "tools/call", statusCall)
					if sub, _ := resp.errorData(); code != http.StatusOK || resp.Error == nil || resp.Error.Code != -32098 || sub != "no_identity" {
						t.Fatalf("tools/call asserted as %s: status=%d error=%+v sub_reason=%q, want -32098 no_identity", mcpAssertedWorker, code, resp.Error, sub)
					}
				}
				mcpAssertedAuth(t, p, mcpAssertedRequest(http.MethodPost, "/mcp/message", "", mcpAssertedBoundKey, mcpAssertedApprover), mcpAssertedWorker, false)
				code, resp := mcpAssertedRPC(t, srv.URL, mcpAssertedBoundKey, mcpAssertedApprover, "tools/list", `{}`)
				found := false
				for _, tool := range resp.Result.Tools {
					found = found || tool.Name == mcp.ToolStatus
				}
				if code != http.StatusOK || !found {
					t.Fatalf("tools/list with the bound key: status=%d tools=%v, want %s", code, resp.Result.Tools, mcp.ToolStatus)
				}
				code, resp = mcpAssertedRPC(t, srv.URL, mcpAssertedBoundKey, mcpAssertedApprover, "tools/call", statusCall)
				if code != http.StatusOK || (gateOn && resp.Error != nil) || (resp.Error != nil && resp.Error.Code == -32098) {
					t.Fatalf("tools/call with the bound key: status=%d error=%+v, want 200 and no identity refusal", code, resp.Error)
				}
			})
		}
	})
}

func TestMCPAssertedPrincipal_MetadataUsesAuthenticatedPrincipal(t *testing.T) {
	cases := []struct {
		name, key, header, principal, agent string
		asserted                            bool
	}{
		{name: "unbound key asserting the linked worker", key: mcpAssertedUnboundKey, header: mcpAssertedWorker, principal: mcpAssertedWorker, asserted: true},
		{name: "managed key asserting the linked worker", key: mcpAssertedManagedKey, header: mcpAssertedWorker, principal: mcpAssertedWorker, asserted: true},
		{name: "bound key with another header", key: mcpAssertedBoundKey, header: mcpAssertedApprover, principal: mcpAssertedWorker, agent: mcpAssertedAgent},
	}
	mcpAssertedProfiles(t, func(t *testing.T, env map[string]string) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s, p := mcpAssertedGateway(t, env, false)
				var gateMeta MCPCallMetadata
				var callMeta mcp.CallMetadata
				var identity *mcp.AgentIdentity
				handler := s.mcpAuth(func(w http.ResponseWriter, r *http.Request) {
					gateMeta, _ = MCPCallMetadataFromContext(r.Context())
					callMeta, _ = mcp.CallMetadataFromContext(r.Context())
					identity = mcp.IdentityFromContext(r.Context())
					w.WriteHeader(http.StatusNoContent)
				})
				req := mcpAssertedRequest(http.MethodPost, "/mcp/message", "", tc.key, tc.header)
				req.Header.Set("X-MCP-Session-ID", "sess-asserted")
				mcpAssertedAuth(t, p, req, tc.principal, tc.asserted)
				rr := httptest.NewRecorder()
				handler(rr, req)
				if rr.Code != http.StatusNoContent {
					t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
				}
				wantPrincipal := tc.principal
				if tc.asserted {
					wantPrincipal = ""
				}
				if gateMeta.Principal != wantPrincipal || callMeta.Principal != wantPrincipal {
					t.Fatalf("Principal gate=%q call=%q, want %q", gateMeta.Principal, callMeta.Principal, wantPrincipal)
				}
				if gateMeta.AgentID != tc.agent || callMeta.AgentID != tc.agent {
					t.Fatalf("AgentID gate=%q call=%q, want %q", gateMeta.AgentID, callMeta.AgentID, tc.agent)
				}
				if (tc.agent == "" && identity != nil) || (tc.agent != "" && (identity == nil || identity.ID != tc.agent)) {
					t.Fatalf("identity = %+v, want %q", identity, tc.agent)
				}
				if want := apiKeyIdentity(tc.key) + "|principal:" + tc.principal; gateMeta.RequesterIdentity != want {
					t.Fatalf("RequesterIdentity = %q, want %q", gateMeta.RequesterIdentity, want)
				}
			})
		}
	})
}

func TestMCPAssertedPrincipal_ApprovalRequesterIsAuthenticated(t *testing.T) {
	createCall := `{"name":"` + mcp.ToolCreateWorkflow + `","arguments":{"name":"asserted-principal-check"}}`
	mcpAssertedProfiles(t, func(t *testing.T, env map[string]string) {
		s, p := mcpAssertedGateway(t, env, false)
		srv := mcpAssertedServe(t, s)
		approvals := s.getMCPRuntime().approvalStore
		for _, key := range []string{mcpAssertedUnboundKey, mcpAssertedManagedKey} {
			code, resp := mcpAssertedRPC(t, srv.URL, key, mcpAssertedWorker, "tools/call", createCall)
			if sub, _ := resp.errorData(); code != http.StatusOK || resp.Error == nil || resp.Error.Code != -32098 || sub != "no_identity" {
				t.Fatalf("create_workflow asserted as %s: status=%d error=%+v sub_reason=%q, want -32098 no_identity", mcpAssertedWorker, code, resp.Error, sub)
			}
			if recs := pendingMCPApprovals(t, approvals, mcp.ToolCreateWorkflow); len(recs) != 0 {
				t.Fatalf("asserted principal enqueued %d approval(s); first requester %q principal %q", len(recs), recs[0].Requester, recs[0].Principal)
			}
		}
		code, resp := mcpAssertedRPC(t, srv.URL, mcpAssertedBoundKey, mcpAssertedApprover, "tools/call", createCall)
		recs := pendingMCPApprovals(t, approvals, mcp.ToolCreateWorkflow)
		if len(recs) != 1 {
			t.Fatalf("pending create_workflow approvals = %d (status=%d error=%+v), want 1", len(recs), code, resp.Error)
		}
		rec := recs[0]
		wantRequester := apiKeyIdentity(mcpAssertedBoundKey) + "|principal:" + mcpAssertedWorker
		if rec.AgentID != mcpAssertedAgent || rec.Principal != mcpAssertedWorker || rec.Requester != wantRequester {
			t.Fatalf("approval AgentID=%q Principal=%q Requester=%q, want %q/%q/%q", rec.AgentID, rec.Principal, rec.Requester, mcpAssertedAgent, mcpAssertedWorker, wantRequester)
		}
		mcpAssertedCheckApprovalGuard(t, s, p, approvals, rec.ID)
	})
}

func mcpAssertedCheckApprovalGuard(t *testing.T, s *server, p *auth.BasicAuthProvider, approvals *MCPApprovalStore, id string) {
	t.Helper()
	for _, approver := range []struct {
		name, key, header string
		asserted          bool
	}{
		{"same principal through another bound key", mcpAssertedBoundApproverKey, mcpAssertedApprover, false},
		{"unbound key asserting the requester", mcpAssertedUnboundKey, mcpAssertedWorker, true},
	} {
		got := mcpAssertedApprove(t, s, p, id, approver.key, approver.header, approver.asserted)
		if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "self_approval_denied") {
			t.Fatalf("%s: status=%d body=%s, want 403 self_approval_denied", approver.name, got.Code, got.Body.String())
		}
	}
	if still, err := approvals.Get(context.Background(), id); err != nil || still == nil || still.Status != model.ApprovalStatusPending {
		t.Fatalf("after refused approvals: record=%+v err=%v, want pending", still, err)
	}
	got := mcpAssertedApprove(t, s, p, id, mcpAssertedOtherApproverKey, "", false)
	if got.Code != http.StatusOK {
		t.Fatalf("approval by %s: status=%d body=%s, want 200", mcpAssertedApprover, got.Code, got.Body.String())
	}
	if done, err := approvals.Get(context.Background(), id); err != nil || done == nil || done.Status != model.ApprovalStatusApproved {
		t.Fatalf("after approval: record=%+v err=%v, want approved", done, err)
	}
}
