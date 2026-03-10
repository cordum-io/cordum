package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	wf "github.com/cordum/cordum/core/workflow"
)

// seedChatRun creates a workflow + run owned by the given org for chat tests.
func seedChatRun(t *testing.T, s *server, runID, wfID, orgID string) {
	t.Helper()
	ctx := context.Background()
	if err := s.workflowStore.SaveWorkflow(ctx, &wf.Workflow{ID: wfID, OrgID: orgID, Name: "chat-test"}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := s.workflowStore.CreateRun(ctx, &wf.WorkflowRun{ID: runID, WorkflowID: wfID, OrgID: orgID, Status: "running"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
}

func postChat(t *testing.T, s *server, runID string, authCtx *AuthContext, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow-runs/"+runID+"/chat", bytes.NewReader(data))
	req.Header.Set("X-Tenant-ID", authCtx.Tenant)
	req.SetPathValue("id", runID)
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
	rec := httptest.NewRecorder()
	s.handlePostRunChat(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// Role matrix: viewer → 403, operator → 200, admin → 200
// ---------------------------------------------------------------------------

func TestPostRunChat_ViewerDenied(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "viewer"}
	seedChatRun(t, s, "run-v", "wf-v", "default")

	ac := &AuthContext{Tenant: "default", Role: "viewer", PrincipalID: "u1"}
	rec := postChat(t, s, "run-v", ac, map[string]string{"content": "hello", "role": "user"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPostRunChat_OperatorAllowed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "operator"}
	seedChatRun(t, s, "run-op", "wf-op", "default")

	ac := &AuthContext{Tenant: "default", Role: "operator", PrincipalID: "u2"}
	rec := postChat(t, s, "run-op", ac, map[string]string{"content": "hello"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for operator, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPostRunChat_AdminAllowed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "admin"}
	seedChatRun(t, s, "run-ad", "wf-ad", "default")

	ac := &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "u3"}
	rec := postChat(t, s, "run-ad", ac, map[string]string{"content": "hello"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Role injection: operator cannot impersonate agent/system
// ---------------------------------------------------------------------------

func TestPostRunChat_OperatorForcedToUser(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "operator"}
	seedChatRun(t, s, "run-oi", "wf-oi", "default")

	for _, injectedRole := range []string{"agent", "system", "assistant"} {
		ac := &AuthContext{Tenant: "default", Role: "operator", PrincipalID: "op1"}
		rec := postChat(t, s, "run-oi", ac, map[string]string{"content": "test", "role": injectedRole})
		if rec.Code != http.StatusOK {
			t.Fatalf("role=%s: expected 200, got %d: %s", injectedRole, rec.Code, rec.Body.String())
		}
		var msg chatMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
			t.Fatalf("role=%s: decode: %v", injectedRole, err)
		}
		if msg.Role != "user" {
			t.Fatalf("role=%s: expected stored role=user, got %q", injectedRole, msg.Role)
		}
	}
}

func TestPostRunChat_AdminCanSetAgent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "admin"}
	seedChatRun(t, s, "run-aa", "wf-aa", "default")

	ac := &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin1"}
	rec := postChat(t, s, "run-aa", ac, map[string]string{"content": "agent msg", "role": "agent"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var msg chatMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg.Role != "agent" {
		t.Fatalf("expected role=agent for admin, got %q", msg.Role)
	}
}

func TestPostRunChat_AdminCanSetSystem(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "admin"}
	seedChatRun(t, s, "run-as", "wf-as", "default")

	ac := &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin1"}
	rec := postChat(t, s, "run-as", ac, map[string]string{"content": "sys msg", "role": "system"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var msg chatMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg.Role != "system" {
		t.Fatalf("expected role=system for admin, got %q", msg.Role)
	}
}

// ---------------------------------------------------------------------------
// Invalid role: unrecognized values are rejected with 400
// ---------------------------------------------------------------------------

func TestPostRunChat_InvalidRole_Rejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "admin"}
	seedChatRun(t, s, "run-ir", "wf-ir", "default")

	for _, badRole := range []string{"root", "superadmin", "moderator"} {
		ac := &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin1"}
		rec := postChat(t, s, "run-ir", ac, map[string]string{"content": "test", "role": badRole})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("role=%s: expected 400, got %d: %s", badRole, rec.Code, rec.Body.String())
		}
	}
}

func TestPostRunChat_EmptyRole_DefaultsToUser(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "operator"}
	seedChatRun(t, s, "run-er", "wf-er", "default")

	ac := &AuthContext{Tenant: "default", Role: "operator", PrincipalID: "op1"}
	rec := postChat(t, s, "run-er", ac, map[string]string{"content": "hi"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var msg chatMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg.Role != "user" {
		t.Fatalf("expected default role=user, got %q", msg.Role)
	}
}

// ---------------------------------------------------------------------------
// Cross-tenant: operator in tenant-a cannot post to tenant-b run
// ---------------------------------------------------------------------------

func TestPostRunChat_CrossTenant_Denied(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "tenant-a", role: "admin"}
	seedChatRun(t, s, "run-ct", "wf-ct", "tenant-b")

	ac := &AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "admin-a"}
	rec := postChat(t, s, "run-ct", ac, map[string]string{"content": "cross-tenant"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Empty content: should be rejected
// ---------------------------------------------------------------------------

func TestPostRunChat_EmptyContent_Rejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "admin"}
	seedChatRun(t, s, "run-ec", "wf-ec", "default")

	ac := &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin1"}
	rec := postChat(t, s, "run-ec", ac, map[string]string{"content": "  ", "role": "user"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty content, got %d: %s", rec.Code, rec.Body.String())
	}
}
