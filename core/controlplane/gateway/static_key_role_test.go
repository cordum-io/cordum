package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
)

// TestStaticKeyRoleKeptAsWritten: a static CORDUM_API_KEYS entry keeps the
// role operator as written instead of normalizing it to admin (credential-model
// ruling A, task-18bdea37). Under the RBAC entitlement an operator key is
// refused agents.write and config.write, and still creates a workflow
// (workflows.write plus the legacy admin gate, which treats operator as admin).
// secops stays an admin alias.
func TestStaticKeyRoleKeptAsWritten(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(e *licensing.Entitlements) {
		e.RBAC = true
		e.AgentIdentity = true
	})
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"operator-key","role":"Operator","tenant":"default"},` +
			`{"key":"secops-key","role":"secops","tenant":"default"},{"key":"viewer-key","role":"viewer","tenant":"default"}]`,
	})
	authed := func(t *testing.T, method, path, key, body string) *http.Request {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-ID", "default")
		req.Header.Set("X-API-Key", key)
		ac, err := s.auth.AuthenticateHTTP(req)
		if err != nil {
			t.Fatalf("authenticate %s: %v", key, err)
		}
		return withAuth(req, ac)
	}
	for key, want := range map[string]string{"operator-key": "operator", "secops-key": "admin", "viewer-key": "viewer"} {
		if got := auth.FromRequest(authed(t, http.MethodGet, "/api/v1/status", key, "")).Role; got != want {
			t.Errorf("%s authenticates with role %q, want %q", key, got, want)
		}
	}

	identity, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		TenantID: "default", Name: "governed-bot", Owner: "ops", RiskTier: "high", Status: "active",
	})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	req := authed(t, http.MethodPut, "/api/v1/agents/"+identity.ID, "operator-key", `{"preapproved_mutating_tools":["cordum_create_workflow"]}`)
	req.SetPathValue("id", identity.ID)
	rr := httptest.NewRecorder()
	s.handleUpdateAgent(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("operator key PUT /api/v1/agents/{id}: %d %s, want 403 (agents.write)", rr.Code, rr.Body.String())
	}
	if got, err := s.agentIdentityStore.Get(context.Background(), "default", identity.ID); err != nil || got == nil || len(got.PreapprovedMutatingTools) != 0 {
		t.Errorf("identity after the refused update: %+v (err %v), want no preapproved tools", got, err)
	}

	const config = `{"mcp":{"enabled":true}}`
	for key, want := range map[string]int{"operator-key": http.StatusForbidden, "secops-key": http.StatusNoContent} {
		rr = httptest.NewRecorder()
		s.handleSetConfig(rr, authed(t, http.MethodPut, "/api/v1/config", key, config))
		if rr.Code != want {
			t.Errorf("%s PUT /api/v1/config: %d %s, want %d", key, rr.Code, rr.Body.String(), want)
		}
	}

	rr = httptest.NewRecorder()
	s.handleCreateWorkflow(rr, authed(t, http.MethodPost, "/api/v1/workflows", "operator-key",
		`{"id":"wf-operator","org_id":"default","name":"Operator workflow","steps":{"s1":{"type":"delay","delay_sec":1}}}`))
	if rr.Code != http.StatusCreated {
		t.Errorf("operator key POST /api/v1/workflows: %d %s, want 201 (workflows.write)", rr.Code, rr.Body.String())
	}

	// Without the RBAC entitlement the legacy role gates still admit operator.
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(e *licensing.Entitlements) {
		e.RBAC = false
		e.AgentIdentity = true
	})
	rr = httptest.NewRecorder()
	s.handleSetConfig(rr, authed(t, http.MethodPut, "/api/v1/config", "operator-key", config))
	if rr.Code != http.StatusNoContent {
		t.Errorf("operator key PUT /api/v1/config without RBAC: %d %s, want 204", rr.Code, rr.Body.String())
	}
}
