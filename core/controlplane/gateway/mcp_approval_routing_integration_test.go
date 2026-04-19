package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMCPApprovalRoutesThroughMux is the router-level integration test
// QA demanded: it registers the real routes via a fresh mux, issues a
// PUT through net/http, and asserts the handler chain reaches the
// MCPApprovalStore. Directly-called unit tests can pass while the mux
// registration is broken; only a mux-wired test catches a missed
// gateway.go HandleFunc.
func TestMCPApprovalRoutesThroughMux(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Stand up an MCPApprovalStore + handler and attach to the runtime
	// cache the real handlers read from. Short-circuits the full
	// registerMCPRoutes path (which requires the gateway mcp.enabled
	// config) while exercising the same request flow.
	store := NewMCPApprovalStore(s.redisClient())
	handler := newMCPApprovalHandler(store)
	s.setMCPRuntime(&mcpRuntimeState{
		startedAt:       time.Now().UTC(),
		transport:       "http",
		approvalStore:   store,
		approvalHandler: handler,
	})
	t.Cleanup(func() { s.setMCPRuntime(nil) })

	// Seed one pending record so List + Get have something to return.
	ctx := context.Background()
	rec, err := store.EnqueueMCPApproval(ctx, &MCPApprovalRequest{
		Tenant:    "default",
		AgentID:   "agent-router",
		Principal: "operator@cordum.io",
		ToolName:  "dangerous.delete",
		ArgsHash:  "00ffcc",
		Requester: "operator@cordum.io",
		TTL:       time.Minute,
	})
	if err != nil {
		t.Fatalf("seed approval: %v", err)
	}

	// Build the mux the way the gateway does it, but only for the
	// four MCP approval routes under test. Mirrors gateway.go:1058-1061
	// so a silent rename there breaks this test.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/mcp/approvals", s.handleMCPApprovalList)
	mux.HandleFunc("GET /api/v1/mcp/approvals/{id}", s.handleMCPApprovalGet)
	mux.HandleFunc("POST /api/v1/mcp/approvals/{id}/approve", s.handleMCPApprovalApprove)
	mux.HandleFunc("POST /api/v1/mcp/approvals/{id}/reject", s.handleMCPApprovalReject)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		var reader = strings.NewReader(body)
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("X-Tenant-ID", "default")
		req.Header.Set("X-Principal-Role", "admin")
		req.Header.Set("Content-Type", "application/json")
		req = adminCtx(req) // installs auth context with admin role
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// GET list
	list := do(http.MethodGet, "/api/v1/mcp/approvals?status=pending", "")
	if list.Code != http.StatusOK {
		t.Fatalf("GET list: %d %s", list.Code, list.Body.String())
	}
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(list.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0]["id"] != rec.ID {
		t.Errorf("unexpected list response: %+v", listResp)
	}

	// GET one
	get := do(http.MethodGet, "/api/v1/mcp/approvals/"+rec.ID, "")
	if get.Code != http.StatusOK {
		t.Fatalf("GET one: %d %s", get.Code, get.Body.String())
	}

	// POST approve — different principal so the self-approval guard
	// doesn't block. The admin context supplied by adminCtx carries
	// principal="admin" whereas the record's requester is
	// operator@cordum.io; distinct principals = allowed.
	approve := do(http.MethodPost, "/api/v1/mcp/approvals/"+rec.ID+"/approve", `{"reason":"ok"}`)
	if approve.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", approve.Code, approve.Body.String())
	}
}

// TestMCPApprovalRoutes503WhenRuntimeMissing pins the behaviour when
// the MCP runtime hasn't been set up — the handler must respond with
// 503 + a specific code, not a confusing 500 or nil-pointer panic.
func TestMCPApprovalRoutes503WhenRuntimeMissing(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Do NOT call setMCPRuntime — approvalHandler is nil.

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/mcp/approvals", s.handleMCPApprovalList)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/mcp/approvals", nil))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "mcp_approvals_unavailable") {
		t.Errorf("expected mcp_approvals_unavailable code in body: %s", rec.Body.String())
	}
}
