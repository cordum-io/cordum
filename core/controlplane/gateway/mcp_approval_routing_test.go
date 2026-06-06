package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
)

// routingTestServer stands up a minimal *server with only the bits the
// MCP approval router shims need: an MCP runtime carrying the handler,
// and an auth that produces the matching tenant/principal on each
// request. The gateway.go route registration is NOT exercised here —
// see TestMCPApprovalRouter_RoutesRegistered for that — this helper
// lets each test call the shim directly with a fully populated request.
func routingTestServer(t *testing.T) (*server, *MCPApprovalStore) {
	t.Helper()
	store := newTestMCPStore(t)
	s := &server{}
	s.auth = &policySimAuth{}
	handler := newMCPApprovalHandler(store)
	handler.getApproverIdentity = func(r *http.Request) string {
		auth := auth.FromRequest(r)
		if auth == nil {
			return ""
		}
		return "apikey:test|principal:" + strings.TrimSpace(auth.PrincipalID)
	}
	handler.approverPrincipalID = func(r *http.Request) string {
		auth := auth.FromRequest(r)
		if auth == nil {
			return ""
		}
		return strings.TrimSpace(auth.PrincipalID)
	}
	handler.approverRole = func(r *http.Request) string {
		auth := auth.FromRequest(r)
		if auth == nil {
			return ""
		}
		return strings.TrimSpace(auth.Role)
	}
	s.setMCPRuntime(&mcpRuntimeState{
		approvalStore:   store,
		approvalHandler: handler,
	})
	t.Cleanup(func() { s.clearMCPRuntime() })
	return s, store
}

func enqueueTestApproval(t *testing.T, store *MCPApprovalStore, requester string, tenantOverride ...string) *MCPApprovalRecord {
	t.Helper()
	tenant := "default"
	if len(tenantOverride) > 0 {
		tenant = tenantOverride[0]
	}
	rec, err := store.EnqueueMCPApproval(context.Background(), &MCPApprovalRequest{
		Tenant:    tenant,
		AgentID:   "agent-alpha",
		ToolName:  "files.delete",
		ArgsHash:  "h",
		Requester: requester,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func signRequest(tenant, principal, role string, allowCrossTenant ...bool) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/approvals/x/approve", nil)
	r.Header.Set("X-Tenant-ID", tenant)
	r.Header.Set("X-Principal-Id", principal)
	r.Header.Set("X-Principal-Role", role)
	allow := false
	if len(allowCrossTenant) > 0 {
		allow = allowCrossTenant[0]
	}
	return withAuth(r, &auth.AuthContext{Tenant: tenant, PrincipalID: principal, Role: role, AllowCrossTenant: allow})
}

func TestMCPApprovalRouter_UnavailableReturns503(t *testing.T) {
	t.Parallel()
	s := &server{auth: &policySimAuth{}}
	rec := httptest.NewRecorder()
	// No MCP runtime set — the shim must respond 503 rather than panic.
	r := signRequest("default", "alice", "admin")
	s.handleMCPApprovalList(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "mcp_approvals_unavailable") {
		t.Errorf("error code missing: %s", rec.Body.String())
	}
}

func TestMCPApprovalRouter_AdminOnlyForApprove(t *testing.T) {
	t.Parallel()
	s, store := routingTestServer(t)
	approval := enqueueTestApproval(t, store, "agent-alpha")

	// Non-admin caller cannot approve. `operator` role normalises to
	// `admin` in this codebase (auth/helpers.go NormalizeRole), so use
	// `viewer` to exercise the 403 path.
	r := signRequest("default", "bob", "viewer")
	r.URL.Path = "/api/v1/mcp/approvals/" + approval.ID + "/approve"
	r.SetPathValue("id", approval.ID)
	rec := httptest.NewRecorder()
	s.handleMCPApprovalApprove(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMCPApprovalRouter_AdminCanApprove(t *testing.T) {
	t.Parallel()
	s, store := routingTestServer(t)
	approval := enqueueTestApproval(t, store, "agent-alpha")

	body, _ := json.Marshal(map[string]string{"reason": "ok"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/approvals/"+approval.ID+"/approve", bytes.NewReader(body))
	r.Header.Set("X-Tenant-ID", "default")
	r.Header.Set("X-Principal-Id", "admin-1")
	r.Header.Set("X-Principal-Role", "admin")
	r = withAuth(r, &auth.AuthContext{Tenant: "default", PrincipalID: "admin-1", Role: "admin"})
	r.SetPathValue("id", approval.ID)

	rec := httptest.NewRecorder()
	s.handleMCPApprovalApprove(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out MCPApprovalRecord
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Status != model.ApprovalStatusApproved {
		t.Errorf("status = %q want approved", out.Status)
	}
}

func TestMCPApprovalRouter_SelfApprovalBlocked(t *testing.T) {
	t.Parallel()
	s, store := routingTestServer(t)
	// Record requester == "alice" (what the gate would store from Principal).
	approval := enqueueTestApproval(t, store, "alice")

	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/approvals/"+approval.ID+"/approve", nil)
	r.Header.Set("X-Tenant-ID", "default")
	r.Header.Set("X-Principal-Id", "alice")
	r.Header.Set("X-Principal-Role", "admin")
	r = withAuth(r, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	r.SetPathValue("id", approval.ID)

	rec := httptest.NewRecorder()
	s.handleMCPApprovalApprove(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 self-approval, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "self_approval_denied") {
		t.Errorf("want self_approval_denied code, got %s", rec.Body.String())
	}
}

func TestMCPApprovalRouter_ListFiltersByTenant(t *testing.T) {
	t.Parallel()
	s, store := routingTestServer(t)
	_, _ = store.EnqueueMCPApproval(context.Background(), &MCPApprovalRequest{
		Tenant: "default", AgentID: "a", ToolName: "t1", ArgsHash: "h1",
	})
	_, _ = store.EnqueueMCPApproval(context.Background(), &MCPApprovalRequest{
		Tenant: "other", AgentID: "a", ToolName: "t1", ArgsHash: "h2",
	})

	r := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/approvals?status=pending", nil)
	r.Header.Set("X-Tenant-ID", "default")
	r.Header.Set("X-Principal-Id", "alice")
	r.Header.Set("X-Principal-Role", "admin")
	r = withAuth(r, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})

	rec := httptest.NewRecorder()
	s.handleMCPApprovalList(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []*MCPApprovalRecord `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	// default tenant request must not see the "other" tenant's record.
	for _, rec := range out.Items {
		if rec.Tenant != "default" {
			t.Errorf("cross-tenant leak: got tenant %q", rec.Tenant)
		}
	}
}

func TestMCPApprovalResolve_CrossTenantDenied(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"approve", "reject"} {
		t.Run(action, func(t *testing.T) {
			s, store := routingTestServer(t)
			approval := enqueueTestApproval(t, store, "tenant-b-requester", "other")
			r := signRequest("default", "tenant-a-operator", "admin")
			r.URL.Path = "/api/v1/mcp/approvals/" + approval.ID + "/" + action
			r.SetPathValue("id", approval.ID)

			rec := httptest.NewRecorder()
			if action == "approve" {
				s.handleMCPApprovalApprove(rec, r)
			} else {
				s.handleMCPApprovalReject(rec, r)
			}

			assertCodedError(t, rec, http.StatusNotFound, "approval_not_found")
			stored, err := store.Get(context.Background(), approval.ID)
			if err != nil {
				t.Fatalf("load stored approval: %v", err)
			}
			if stored.Status != model.ApprovalStatusPending {
				t.Fatalf("status = %q, want pending", stored.Status)
			}
		})
	}
}

func TestMCPApprovalResolve_SameTenantStillWorks(t *testing.T) {
	t.Parallel()
	s, store := routingTestServer(t)
	approval := enqueueTestApproval(t, store, "agent-alpha")
	r := signRequest("default", "same-tenant-admin", "admin")
	r.URL.Path = "/api/v1/mcp/approvals/" + approval.ID + "/approve"
	r.SetPathValue("id", approval.ID)

	rec := httptest.NewRecorder()
	s.handleMCPApprovalApprove(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := store.Get(context.Background(), approval.ID)
	if err != nil {
		t.Fatalf("load stored approval: %v", err)
	}
	if stored.Status != model.ApprovalStatusApproved {
		t.Fatalf("status = %q, want approved", stored.Status)
	}
}

func TestMCPApprovalResolve_CrossTenantAdminAllowed(t *testing.T) {
	t.Parallel()
	s, store := routingTestServer(t)
	approval := enqueueTestApproval(t, store, "tenant-b-requester", "other")
	r := signRequest("default", "cross-tenant-admin", "admin", true)
	r.URL.Path = "/api/v1/mcp/approvals/" + approval.ID + "/approve"
	r.SetPathValue("id", approval.ID)

	rec := httptest.NewRecorder()
	s.handleMCPApprovalApprove(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := store.Get(context.Background(), approval.ID)
	if err != nil {
		t.Fatalf("load stored approval: %v", err)
	}
	if stored.Status != model.ApprovalStatusApproved {
		t.Fatalf("status = %q, want approved", stored.Status)
	}
}

// TestMCPApprovalRouter_RoutesRegistered confirms that the four route
// strings added to gateway.go section 11.6 are present in the source.
// A pure HTTP-level test is awkward because Go's ServeMux wildcard
// patterns interact with shared-prefix exact-match routes in subtle
// ways depending on test isolation — the source-level check is the
// authoritative signal that a refactor didn't silently drop the routes.
func TestMCPApprovalRouter_RoutesRegistered(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("gateway.go")
	if err != nil {
		t.Fatalf("read gateway.go: %v", err)
	}
	src := string(data)
	want := []string{
		`GET /api/v1/mcp/approvals`,
		`GET /api/v1/mcp/approvals/{id}`,
		`POST /api/v1/mcp/approvals/{id}/approve`,
		`POST /api/v1/mcp/approvals/{id}/reject`,
	}
	for _, pattern := range want {
		if !strings.Contains(src, pattern) {
			t.Errorf("gateway.go missing MCP approval route %q — refactor dropped it?", pattern)
		}
	}
}
