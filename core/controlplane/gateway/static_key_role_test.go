package gateway

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
)

// TestStaticKeyRoleKeptAsWritten: a static CORDUM_API_KEYS entry keeps its
// role as written, so operator and secops are never normalized to admin
// (credential-model ruling A, task-18bdea37; sync ruling, task-a5e2e73e).
// Under the RBAC entitlement an operator key is refused agents.write, and still
// creates a workflow (workflows.write plus the admin role list, which admits
// operator on a permission-checked route). With and without the entitlement,
// operator and secops keys are refused config.write and leave the config
// unchanged, while an admin key's config write succeeds.
func TestStaticKeyRoleKeptAsWritten(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(e *licensing.Entitlements) {
		e.RBAC = true
		e.AgentIdentity = true
	})
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"operator-key","role":"Operator","tenant":"default"},` +
			`{"key":"secops-key","role":"secops","tenant":"default"},{"key":"viewer-key","role":"viewer","tenant":"default"},` +
			`{"key":"admin-key","role":"admin","tenant":"default"}]`,
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
	for key, want := range map[string]string{"operator-key": "operator", "secops-key": "secops", "viewer-key": "viewer"} {
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

	configDoc := func() string {
		doc, err := s.configSvc.Get(context.Background(), "system", "default")
		return fmt.Sprintf("%v %+v", err, doc)
	}
	// putConfig: operator and secops keys are refused config.write and the
	// system/default document stays as it was; then an admin key's PUT answers
	// 204 and changes it. config must differ from the stored document.
	putConfig := func(t *testing.T, state, config string) {
		t.Helper()
		for _, key := range []string{"operator-key", "secops-key"} {
			before := configDoc()
			rr := httptest.NewRecorder()
			s.handleSetConfig(rr, authed(t, http.MethodPut, "/api/v1/config", key, config))
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s PUT /api/v1/config %s: %d %s, want 403", key, state, rr.Code, rr.Body.String())
			}
			if after := configDoc(); after != before {
				t.Errorf("%s PUT /api/v1/config %s changed system/default to %s, want %s", key, state, after, before)
			}
		}
		before := configDoc()
		rr := httptest.NewRecorder()
		s.handleSetConfig(rr, authed(t, http.MethodPut, "/api/v1/config", "admin-key", config))
		if rr.Code != http.StatusNoContent || configDoc() == before {
			t.Errorf("admin key PUT /api/v1/config %s: %d %s, want 204 and a changed system/default", state, rr.Code, rr.Body.String())
		}
	}
	putConfig(t, "with RBAC", `{"mcp":{"enabled":true}}`)

	rr = httptest.NewRecorder()
	s.handleCreateWorkflow(rr, authed(t, http.MethodPost, "/api/v1/workflows", "operator-key",
		`{"id":"wf-operator","org_id":"default","name":"Operator workflow","steps":{"s1":{"type":"delay","delay_sec":1}}}`))
	if rr.Code != http.StatusCreated {
		t.Errorf("operator key POST /api/v1/workflows: %d %s, want 201 (workflows.write)", rr.Code, rr.Body.String())
	}

	// Without the RBAC entitlement the built-in role permissions apply: operator
	// holds no config.write and secops holds no permission.
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(e *licensing.Entitlements) {
		e.RBAC = false
		e.AgentIdentity = true
	})
	putConfig(t, "without RBAC", `{"mcp":{"enabled":false}}`)
}
