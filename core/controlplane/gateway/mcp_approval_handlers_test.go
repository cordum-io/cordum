package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

// newTestMCPHandler builds an mcpApprovalHandler backed by miniredis
// with deterministic approver-identity stubs. Tests can override the
// stub to simulate different principals.
func newTestMCPHandler(t *testing.T, approverPrincipal string) *mcpApprovalHandler {
	t.Helper()
	store := newTestMCPStore(t)
	return &mcpApprovalHandler{
		store: store,
		getApproverIdentity: func(_ *http.Request) string {
			if approverPrincipal == "" {
				return ""
			}
			return "apikey:aaaaaaaa|principal:" + approverPrincipal
		},
		approverPrincipalID: func(_ *http.Request) string { return approverPrincipal },
		approverRole:        func(_ *http.Request) string { return "admin" },
	}
}

// seedPending enqueues a fresh approval for the given (agent, tool).
// The Requester is the agent — matches what gatewayApprovalGate.Check
// records.
func seedPending(t *testing.T, h *mcpApprovalHandler, agent, tool string) *MCPApprovalRecord {
	t.Helper()
	rec, err := h.store.EnqueueMCPApproval(context.Background(), &MCPApprovalRequest{
		Tenant:    "default",
		AgentID:   agent,
		ToolName:  tool,
		ArgsHash:  "hash-" + agent,
		Requester: agent,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return rec
}

func withDefaultMCPApprovalAuth(req *http.Request, principal string) *http.Request {
	return withAuth(req, &auth.AuthContext{Tenant: "default", PrincipalID: principal, Role: "admin"})
}

// TestApproveRejectsSelfApproval is the canonical plan-6 test: the
// approver's principal equals the approval's requester → 403 with
// code=self_approval_denied. No resolution happens — the record stays
// PENDING.
func TestApproveRejectsSelfApproval(t *testing.T) {
	t.Parallel()
	h := newTestMCPHandler(t, "agent-1") // approver == requester
	rec := seedPending(t, h, "agent-1", "files.delete")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/mcp/"+rec.ID+"/approve", strings.NewReader(`{"reason":"lgtm"}`))
	req = withDefaultMCPApprovalAuth(req, "agent-1")
	rr := httptest.NewRecorder()
	h.Approve(rr, req, rec.ID)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if code, _ := body["code"].(string); code != "self_approval_denied" {
		t.Errorf("code = %v, want self_approval_denied", body["code"])
	}

	// Record must still be PENDING — the 403 is a hard refusal, not a reject.
	got, err := h.store.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.ApprovalStatusPending {
		t.Errorf("status = %q, want pending", got.Status)
	}
}

func TestMCPApprovalRejectsSameKeyDifferentPrincipalComposite(t *testing.T) {
	t.Parallel()
	h := newTestMCPHandler(t, "")
	h.getApproverIdentity = func(_ *http.Request) string {
		return "apikey:sharedhash|principal:bob"
	}
	h.approverPrincipalID = func(_ *http.Request) string { return "bob" }
	rec := seedPending(t, h, "agent-1", "files.delete")
	rec.Requester = "apikey:sharedhash|principal:alice"
	mustWriteMCPApprovalRecord(t, h.store, rec)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/mcp/"+rec.ID+"/approve", strings.NewReader(`{"reason":"lgtm"}`))
	req = withDefaultMCPApprovalAuth(req, "bob")
	rr := httptest.NewRecorder()
	h.Approve(rr, req, rec.ID)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "self_approval_denied") {
		t.Fatalf("body = %s, want self_approval_denied", rr.Body.String())
	}
}

func TestMCPApprovalRejectsSamePrincipalDifferentKeyComposite(t *testing.T) {
	t.Parallel()
	h := newTestMCPHandler(t, "")
	h.getApproverIdentity = func(_ *http.Request) string {
		return "apikey:approverhash|principal:alice"
	}
	h.approverPrincipalID = func(_ *http.Request) string { return "alice" }
	rec := seedPending(t, h, "agent-1", "files.delete")
	rec.Requester = "apikey:requesterhash|principal:alice"
	mustWriteMCPApprovalRecord(t, h.store, rec)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/mcp/"+rec.ID+"/approve", strings.NewReader(`{"reason":"lgtm"}`))
	req = withDefaultMCPApprovalAuth(req, "alice")
	rr := httptest.NewRecorder()
	h.Approve(rr, req, rec.ID)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "self_approval_denied") {
		t.Fatalf("body = %s, want self_approval_denied", rr.Body.String())
	}
}

func TestMCPApprovalHandlers_NoRawErrorLeak(t *testing.T) {
	t.Parallel()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	_ = client.Close()
	h := newMCPApprovalHandler(NewMCPApprovalStore(client))

	for _, tc := range []struct {
		name string
		run  func(*httptest.ResponseRecorder)
	}{
		{"get", func(rr *httptest.ResponseRecorder) {
			req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/mcp/approvals/appr", nil), &auth.AuthContext{Tenant: "default"})
			h.Get(rr, req, "appr")
		}},
		{"list", func(rr *httptest.ResponseRecorder) {
			req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/mcp/approvals", nil), &auth.AuthContext{Tenant: "default"})
			h.List(rr, req)
		}},
		{"resolve_get", func(rr *httptest.ResponseRecorder) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/approvals/appr/approve", nil)
			h.Approve(rr, req, "appr")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.run(rr)
			if strings.Contains(rr.Body.String(), "client is closed") {
				t.Fatalf("%s leaked raw redis error: %s", tc.name, rr.Body.String())
			}
		})
	}
}

func mustWriteMCPApprovalRecord(t *testing.T, store *MCPApprovalStore, rec *MCPApprovalRecord) {
	t.Helper()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal mcp approval: %v", err)
	}
	if err := store.client.Set(context.Background(), mcpApprovalKey(rec.ID), raw, 0).Err(); err != nil {
		t.Fatalf("write mcp approval: %v", err)
	}
}

// TestApproveSucceedsForDifferentPrincipal confirms the happy path — a
// different admin approves the call and the record flips to APPROVED.
func TestApproveSucceedsForDifferentPrincipal(t *testing.T) {
	t.Parallel()
	h := newTestMCPHandler(t, "admin-1") // approver != requester
	rec := seedPending(t, h, "agent-1", "files.delete")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/mcp/"+rec.ID+"/approve", strings.NewReader(`{"reason":"reviewed"}`))
	req = withDefaultMCPApprovalAuth(req, "admin-1")
	rr := httptest.NewRecorder()
	h.Approve(rr, req, rec.ID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body=%s", rr.Code, rr.Body.String())
	}
	got, err := h.store.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.ApprovalStatusApproved {
		t.Errorf("status = %q, want approved", got.Status)
	}
	if got.ResolvedBy != "admin-1" {
		t.Errorf("resolved_by = %q", got.ResolvedBy)
	}
	// Resolver's free-form reason lands in ResolutionReason so the
	// original trigger reason (stored at enqueue) is never clobbered.
	if got.ResolutionReason != "reviewed" {
		t.Errorf("resolution_reason = %q, want %q", got.ResolutionReason, "reviewed")
	}
}

// TestRejectFlipsToRejected mirrors the approve test but for the reject
// path — ensures the shared resolve() helper dispatches on decision.
func TestRejectFlipsToRejected(t *testing.T) {
	t.Parallel()
	h := newTestMCPHandler(t, "admin-1")
	rec := seedPending(t, h, "agent-1", "files.delete")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/mcp/"+rec.ID+"/reject", strings.NewReader(`{"reason":"too risky"}`))
	req = withDefaultMCPApprovalAuth(req, "admin-1")
	rr := httptest.NewRecorder()
	h.Reject(rr, req, rec.ID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body=%s", rr.Code, rr.Body.String())
	}
	got, err := h.store.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.ApprovalStatusRejected {
		t.Errorf("status = %q, want rejected", got.Status)
	}
}

// TestApprove404ForMissing confirms unknown IDs get a clean 404 rather
// than a leaking internal error. Matches the rest of the approval API.
func TestApprove404ForMissing(t *testing.T) {
	t.Parallel()
	h := newTestMCPHandler(t, "admin-1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/mcp/does-not-exist/approve", nil)
	req = withDefaultMCPApprovalAuth(req, "admin-1")
	rr := httptest.NewRecorder()
	h.Approve(rr, req, "does-not-exist")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if code, _ := body["code"].(string); code != "approval_not_found" {
		t.Errorf("code = %v, want approval_not_found", body["code"])
	}
}

// TestGetReturnsRecord exercises the /api/v1/mcp/approvals/{id} GET.
func TestGetReturnsRecord(t *testing.T) {
	t.Parallel()
	h := newTestMCPHandler(t, "admin-1")
	rec := seedPending(t, h, "agent-1", "files.delete")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/approvals/"+rec.ID, nil)
	// Tenant scoping requires an auth context — without one the handler
	// refuses to disclose cross-tenant records (returns 404). Attach a
	// matching tenant so the read is authorised.
	req = withAuth(req, &auth.AuthContext{Tenant: "default", PrincipalID: "admin-1"})
	rr := httptest.NewRecorder()
	h.Get(rr, req, rec.ID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["id"] != rec.ID {
		t.Errorf("id = %v, want %q", out["id"], rec.ID)
	}
	if out["tool_name"] != "files.delete" {
		t.Errorf("tool_name = %v", out["tool_name"])
	}
}

// TestRequesterMatchesApprover is a small unit test for the helper —
// pins the composite-identity parsing so future changes to the identity
// format don't silently break the guard.
func TestRequesterMatchesApprover(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		requester string
		approver  string
		want      bool
	}{
		{"match by principal", "agent-1", "apikey:abcd|principal:agent-1", true},
		{"different principal", "agent-1", "apikey:abcd|principal:admin", false},
		{"empty requester", "", "principal:admin", false},
		{"empty approver", "agent-1", "", false},
		{"approver without principal segment", "agent-1", "apikey:abcd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requesterMatchesApprover(tc.requester, tc.approver); got != tc.want {
				t.Errorf("requesterMatchesApprover(%q, %q) = %v, want %v", tc.requester, tc.approver, got, tc.want)
			}
		})
	}
}
