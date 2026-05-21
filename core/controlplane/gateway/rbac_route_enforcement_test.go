package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

func putTestRole(t *testing.T, s *server, name string, permissions ...string) {
	t.Helper()
	if s.rbacStore == nil {
		t.Fatal("rbac store unavailable")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{
		Name:        name,
		Description: "test role",
		Permissions: permissions,
		BuiltIn:     false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("put role %s: %v", name, err)
	}
}

func TestRBACRoutePermissions_ConfigAndSchema(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanTeam, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
		entitlements.AgentIdentity = true
	})
	putTestRole(t, s, "config-reader", auth.PermConfigRead, auth.PermSchemasRead)

	getReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/config?scope=system&scope_id=default", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "config-reader",
		PrincipalID: "reader-1",
	})
	getRR := httptest.NewRecorder()
	s.handleGetConfig(getRR, getReq)
	if getRR.Code != http.StatusForbidden {
		t.Fatalf("config-reader read status = %d, want %d body=%s", getRR.Code, http.StatusForbidden, getRR.Body.String())
	}

	adminGetReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/config?scope=system&scope_id=default", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-1",
	})
	adminGetRR := httptest.NewRecorder()
	s.handleGetConfig(adminGetRR, adminGetReq)
	if adminGetRR.Code != http.StatusOK {
		t.Fatalf("admin config read status = %d, want %d body=%s", adminGetRR.Code, http.StatusOK, adminGetRR.Body.String())
	}

	setReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewBufferString(`{"feature":"on"}`)), &auth.AuthContext{
		Tenant:      "default",
		Role:        "config-reader",
		PrincipalID: "reader-1",
	})
	setReq.Header.Set("Content-Type", "application/json")
	setRR := httptest.NewRecorder()
	s.handleSetConfig(setRR, setReq)
	if setRR.Code != http.StatusForbidden {
		t.Fatalf("config write status = %d, want %d body=%s", setRR.Code, http.StatusForbidden, setRR.Body.String())
	}

	listSchemasReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/schemas", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "config-reader",
		PrincipalID: "reader-1",
	})
	listSchemasRR := httptest.NewRecorder()
	s.handleListSchemas(listSchemasRR, listSchemasReq)
	if listSchemasRR.Code != http.StatusForbidden {
		t.Fatalf("config-reader schema list status = %d, want %d body=%s", listSchemasRR.Code, http.StatusForbidden, listSchemasRR.Body.String())
	}

	viewerSchemasReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/schemas", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "viewer",
		PrincipalID: "viewer-1",
	})
	viewerSchemasRR := httptest.NewRecorder()
	s.handleListSchemas(viewerSchemasRR, viewerSchemasReq)
	if viewerSchemasRR.Code != http.StatusOK {
		t.Fatalf("viewer schema list status = %d, want %d body=%s", viewerSchemasRR.Code, http.StatusOK, viewerSchemasRR.Body.String())
	}

	registerSchemaReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/schemas", bytes.NewBufferString(`{"id":"sample","schema":{"type":"object"}}`)), &auth.AuthContext{
		Tenant:      "default",
		Role:        "config-reader",
		PrincipalID: "reader-1",
	})
	registerSchemaReq.Header.Set("Content-Type", "application/json")
	registerSchemaRR := httptest.NewRecorder()
	s.handleRegisterSchema(registerSchemaRR, registerSchemaReq)
	if registerSchemaRR.Code != http.StatusForbidden {
		t.Fatalf("schema register status = %d, want %d body=%s", registerSchemaRR.Code, http.StatusForbidden, registerSchemaRR.Body.String())
	}
}

func TestRBACRoutePermissions_PolicyAndAudit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanTeam, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
		entitlements.AuditExport = true
	})
	putTestRole(t, s, "policy-auditor", auth.PermPolicyRead, auth.PermAuditRead)

	listBundlesReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "policy-auditor",
		PrincipalID: "auditor-1",
	})
	listBundlesRR := httptest.NewRecorder()
	s.handlePolicyBundles(listBundlesRR, listBundlesReq)
	if listBundlesRR.Code != http.StatusForbidden {
		t.Fatalf("policy-auditor bundles status = %d, want %d body=%s", listBundlesRR.Code, http.StatusForbidden, listBundlesRR.Body.String())
	}

	adminBundlesReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-1",
	})
	adminBundlesRR := httptest.NewRecorder()
	s.handlePolicyBundles(adminBundlesRR, adminBundlesReq)
	if adminBundlesRR.Code != http.StatusOK {
		t.Fatalf("admin policy bundles status = %d, want %d body=%s", adminBundlesRR.Code, http.StatusOK, adminBundlesRR.Body.String())
	}

	putBundleReq := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/sample", bytes.NewBufferString(`{"content":"package main\nallow = true"}`)), &auth.AuthContext{
		Tenant:      "default",
		Role:        "policy-auditor",
		PrincipalID: "auditor-1",
	})
	putBundleReq.Header.Set("Content-Type", "application/json")
	putBundleReq.SetPathValue("id", "sample")
	putBundleRR := httptest.NewRecorder()
	s.handlePutPolicyBundle(putBundleRR, putBundleReq)
	if putBundleRR.Code != http.StatusForbidden {
		t.Fatalf("policy bundle write status = %d, want %d body=%s", putBundleRR.Code, http.StatusForbidden, putBundleRR.Body.String())
	}

	auditReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export/config", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "policy-auditor",
		PrincipalID: "auditor-1",
	})
	auditRR := httptest.NewRecorder()
	s.handleAuditExportConfig(auditRR, auditReq)
	if auditRR.Code != http.StatusForbidden {
		t.Fatalf("policy-auditor audit export config status = %d, want %d body=%s", auditRR.Code, http.StatusForbidden, auditRR.Body.String())
	}

	adminAuditReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export/config", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-1",
	})
	adminAuditRR := httptest.NewRecorder()
	s.handleAuditExportConfig(adminAuditRR, adminAuditReq)
	if adminAuditRR.Code != http.StatusOK {
		t.Fatalf("admin audit export config status = %d, want %d body=%s", adminAuditRR.Code, http.StatusOK, adminAuditRR.Body.String())
	}
}

func TestRBACRoutePermissions_ApprovalsAndAgents(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanTeam, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
		entitlements.AgentIdentity = true
	})
	putTestRole(t, s, "reviewer", auth.PermJobsApprove, auth.PermAgentsRead)

	adminCreateReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(`{"name":"rbac-agent","owner":"admin","risk_tier":"low"}`)), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-1",
	})
	adminCreateReq.Header.Set("Content-Type", "application/json")
	adminCreateRR := httptest.NewRecorder()
	s.handleCreateAgent(adminCreateRR, adminCreateReq)
	if adminCreateRR.Code != http.StatusCreated {
		t.Fatalf("admin agent create status = %d, want %d body=%s", adminCreateRR.Code, http.StatusCreated, adminCreateRR.Body.String())
	}

	approvalsReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "reviewer",
		PrincipalID: "reviewer-1",
	})
	approvalsRR := httptest.NewRecorder()
	s.handleListApprovals(approvalsRR, approvalsReq)
	if approvalsRR.Code != http.StatusForbidden {
		t.Fatalf("reviewer approval list status = %d, want %d body=%s", approvalsRR.Code, http.StatusForbidden, approvalsRR.Body.String())
	}

	adminApprovalsReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-1",
	})
	adminApprovalsRR := httptest.NewRecorder()
	s.handleListApprovals(adminApprovalsRR, adminApprovalsReq)
	if adminApprovalsRR.Code != http.StatusOK {
		t.Fatalf("admin approval list status = %d, want %d body=%s", adminApprovalsRR.Code, http.StatusOK, adminApprovalsRR.Body.String())
	}

	listAgentsReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "reviewer",
		PrincipalID: "reviewer-1",
	})
	listAgentsRR := httptest.NewRecorder()
	s.handleListAgents(listAgentsRR, listAgentsReq)
	if listAgentsRR.Code != http.StatusForbidden {
		t.Fatalf("reviewer agent list status = %d, want %d body=%s", listAgentsRR.Code, http.StatusForbidden, listAgentsRR.Body.String())
	}

	adminListAgentsReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-1",
	})
	adminListAgentsRR := httptest.NewRecorder()
	s.handleListAgents(adminListAgentsRR, adminListAgentsReq)
	if adminListAgentsRR.Code != http.StatusOK {
		t.Fatalf("admin agent list status = %d, want %d body=%s", adminListAgentsRR.Code, http.StatusOK, adminListAgentsRR.Body.String())
	}

	createAgentReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(`{"name":"blocked-agent","owner":"reviewer","risk_tier":"low"}`)), &auth.AuthContext{
		Tenant:      "default",
		Role:        "reviewer",
		PrincipalID: "reviewer-1",
	})
	createAgentReq.Header.Set("Content-Type", "application/json")
	createAgentRR := httptest.NewRecorder()
	s.handleCreateAgent(createAgentRR, createAgentReq)
	if createAgentRR.Code != http.StatusForbidden {
		t.Fatalf("agent write status = %d, want %d body=%s", createAgentRR.Code, http.StatusForbidden, createAgentRR.Body.String())
	}
}

func TestRBACRoutePermissions_BackwardCompatibilityWhenDisabled(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanTeam, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = false
	})
	putTestRole(t, s, "config-reader", auth.PermConfigRead, auth.PermSchemasRead)

	getReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/config?scope=system&scope_id=default", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "config-reader",
		PrincipalID: "reader-1",
	})
	getRR := httptest.NewRecorder()
	s.handleGetConfig(getRR, getReq)
	if getRR.Code != http.StatusForbidden {
		t.Fatalf("rbac-off config read status = %d, want %d body=%s", getRR.Code, http.StatusForbidden, getRR.Body.String())
	}

	adminReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/config?scope=system&scope_id=default", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-1",
	})
	adminRR := httptest.NewRecorder()
	s.handleGetConfig(adminRR, adminReq)
	if adminRR.Code != http.StatusOK {
		t.Fatalf("rbac-off admin config read status = %d, want %d body=%s", adminRR.Code, http.StatusOK, adminRR.Body.String())
	}
}
