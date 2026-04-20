package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
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
	if getRR.Code != http.StatusOK {
		t.Fatalf("config read status = %d, want %d body=%s", getRR.Code, http.StatusOK, getRR.Body.String())
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
	if listSchemasRR.Code != http.StatusOK {
		t.Fatalf("schema list status = %d, want %d body=%s", listSchemasRR.Code, http.StatusOK, listSchemasRR.Body.String())
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
	if listBundlesRR.Code != http.StatusOK {
		t.Fatalf("policy bundles status = %d, want %d body=%s", listBundlesRR.Code, http.StatusOK, listBundlesRR.Body.String())
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
	if auditRR.Code != http.StatusOK {
		t.Fatalf("audit export config status = %d, want %d body=%s", auditRR.Code, http.StatusOK, auditRR.Body.String())
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
	if approvalsRR.Code != http.StatusOK {
		t.Fatalf("approval list status = %d, want %d body=%s", approvalsRR.Code, http.StatusOK, approvalsRR.Body.String())
	}

	listAgentsReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "reviewer",
		PrincipalID: "reviewer-1",
	})
	listAgentsRR := httptest.NewRecorder()
	s.handleListAgents(listAgentsRR, listAgentsReq)
	if listAgentsRR.Code != http.StatusOK {
		t.Fatalf("agent list status = %d, want %d body=%s", listAgentsRR.Code, http.StatusOK, listAgentsRR.Body.String())
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

type rbacRouteCase struct {
	name       string
	permission string
	method     string
	url        string
	body       string
	pathValues map[string]string
	handler    func(*server, http.ResponseWriter, *http.Request)
}

func newRBACPermissionServer(t *testing.T) *server {
	t.Helper()

	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
		entitlements.AuditExport = true
		entitlements.LegalHold = true
		entitlements.VelocityRules = true
		entitlements.AgentIdentity = true
	})
	client, ok := s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("expected *redis.Client, got %T", s.jobStore.Client())
	}
	s.keyStore = auth.NewRedisKeyStoreFromClient(client)
	s.legalHoldStore = audit.NewLegalHoldStoreFromClient(client)
	return s
}

func buildRBACRouteRequest(tc rbacRouteCase, role string) *http.Request {
	var body *bytes.Reader
	if tc.body == "" {
		body = bytes.NewReader(nil)
	} else {
		body = bytes.NewReader([]byte(tc.body))
	}
	req := httptest.NewRequest(tc.method, tc.url, body)
	if tc.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range tc.pathValues {
		req.SetPathValue(key, value)
	}
	return withAuth(req, &auth.AuthContext{
		Tenant:      "default",
		Role:        role,
		PrincipalID: role + "-principal",
	})
}

func pathValues(kv ...string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	values := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		values[kv[i]] = kv[i+1]
	}
	return values
}

func TestRBACRoutePermissions_RemainingSensitiveRoutes(t *testing.T) {
	cases := []rbacRouteCase{
		{name: "audit export", permission: auth.PermAuditExport, method: http.MethodGet, url: "/api/v1/audit/export", handler: (*server).handleAuditExport},
		{name: "audit verify", permission: auth.PermAuditVerify, method: http.MethodGet, url: "/api/v1/audit/verify", handler: (*server).handleAuditVerify},
		{name: "list api keys", permission: auth.PermAPIKeysRead, method: http.MethodGet, url: "/api/v1/auth/keys", handler: (*server).handleListKeys},
		{name: "create api key", permission: auth.PermAPIKeysWrite, method: http.MethodPost, url: "/api/v1/auth/keys", body: `{"name":"rbac-key"}`, handler: (*server).handleCreateKey},
		{name: "revoke api key", permission: auth.PermAPIKeysWrite, method: http.MethodDelete, url: "/api/v1/auth/keys/key-1", pathValues: pathValues("id", "key-1"), handler: (*server).handleRevokeKey},
		{name: "workflow run chat", permission: auth.PermWorkflowsWrite, method: http.MethodPost, url: "/api/v1/workflow-runs/run-1/chat", body: `{"message":"hi"}`, pathValues: pathValues("id", "run-1"), handler: (*server).handlePostRunChat},
		{name: "revoke delegation", permission: auth.PermAgentsDelegate, method: http.MethodPost, url: "/api/v1/agents/revoke-delegation", body: `{"jti":"delegation-1"}`, handler: (*server).handleRevokeDelegation},
		{name: "list dlq", permission: auth.PermDLQRead, method: http.MethodGet, url: "/api/v1/dlq", handler: (*server).handleListDLQ},
		{name: "list dlq page", permission: auth.PermDLQRead, method: http.MethodGet, url: "/api/v1/dlq/page", handler: (*server).handleListDLQPage},
		{name: "delete dlq", permission: auth.PermDLQWrite, method: http.MethodDelete, url: "/api/v1/dlq/job-1", pathValues: pathValues("job_id", "job-1"), handler: (*server).handleDeleteDLQ},
		{name: "retry dlq", permission: auth.PermDLQWrite, method: http.MethodPost, url: "/api/v1/dlq/job-1/retry", pathValues: pathValues("job_id", "job-1"), handler: (*server).handleRetryDLQ},
		{name: "get memory", permission: auth.PermMemoryRead, method: http.MethodGet, url: "/api/v1/memory", handler: (*server).handleGetMemory},
		{name: "create legal hold", permission: auth.PermLegalHoldWrite, method: http.MethodPost, url: "/api/v1/audit/legal-hold", body: `{"tenant_id":"default","reason":"retain"}`, handler: (*server).handleCreateLegalHold},
		{name: "list legal holds", permission: auth.PermLegalHoldRead, method: http.MethodGet, url: "/api/v1/audit/legal-holds", handler: (*server).handleListLegalHolds},
		{name: "release legal hold", permission: auth.PermLegalHoldWrite, method: http.MethodDelete, url: "/api/v1/audit/legal-hold/hold-1", pathValues: pathValues("id", "hold-1"), handler: (*server).handleReleaseLegalHold},
		{name: "get license", permission: auth.PermLicenseRead, method: http.MethodGet, url: "/api/v1/license", handler: (*server).handleGetLicense},
		{name: "get license usage", permission: auth.PermLicenseRead, method: http.MethodGet, url: "/api/v1/license/usage", handler: (*server).handleGetLicenseUsage},
		{name: "get lock", permission: auth.PermLocksRead, method: http.MethodGet, url: "/api/v1/locks?resource=job.default", handler: (*server).handleGetLock},
		{name: "mcp outbound", permission: auth.PermMCPRead, method: http.MethodGet, url: "/api/v1/mcp/outbound", handler: (*server).handleMCPOutbound},
		{name: "mcp prompts", permission: auth.PermMCPRead, method: http.MethodGet, url: "/api/v1/mcp/prompts", handler: (*server).handleListMCPPrompts},
		{name: "mcp tools", permission: auth.PermMCPRead, method: http.MethodGet, url: "/api/v1/mcp/tools", handler: (*server).handleListMCPTools},
		{name: "agent tool visibility", permission: auth.PermMCPRead, method: http.MethodGet, url: "/api/v1/agents/agent-1/tools", pathValues: pathValues("id", "agent-1"), handler: (*server).handleAgentToolVisibility},
		{name: "agent denied events", permission: auth.PermMCPRead, method: http.MethodGet, url: "/api/v1/agents/agent-1/denied-events", pathValues: pathValues("id", "agent-1"), handler: (*server).handleAgentDeniedEvents},
		{name: "mcp usage", permission: auth.PermMCPRead, method: http.MethodGet, url: "/api/v1/mcp/usage", handler: (*server).handleMCPUsage},
		{name: "mcp verify signature", permission: auth.PermMCPVerify, method: http.MethodPost, url: "/api/v1/mcp/verify-signature", body: `{"method":"GET","params":{},"headers":{}}`, handler: (*server).handleMCPVerifySignature},
		{name: "list packs", permission: auth.PermPacksRead, method: http.MethodGet, url: "/api/v1/packs", handler: (*server).handleListPacks},
		{name: "get pack", permission: auth.PermPacksRead, method: http.MethodGet, url: "/api/v1/packs/demo", pathValues: pathValues("id", "demo"), handler: (*server).handleGetPack},
		{name: "verify pack", permission: auth.PermPacksVerify, method: http.MethodPost, url: "/api/v1/packs/demo/verify", pathValues: pathValues("id", "demo"), handler: (*server).handleVerifyPack},
		{name: "marketplace packs", permission: auth.PermPacksRead, method: http.MethodGet, url: "/api/v1/marketplace/packs", handler: (*server).handleMarketplacePacks},
		{name: "marketplace install", permission: auth.PermPacksInstall, method: http.MethodPost, url: "/api/v1/marketplace/install", body: `{"id":"demo"}`, handler: (*server).handleMarketplaceInstall},
		{name: "put policy shadow", permission: auth.PermPolicyWrite, method: http.MethodPut, url: "/api/v1/policy/bundles/demo/shadow", body: `{"policy":"package demo\nallow = true"}`, pathValues: pathValues("id", "demo"), handler: (*server).handlePutPolicyShadow},
		{name: "get policy shadow", permission: auth.PermPolicyRead, method: http.MethodGet, url: "/api/v1/policy/bundles/demo/shadow", pathValues: pathValues("id", "demo"), handler: (*server).handleGetPolicyShadow},
		{name: "delete policy shadow", permission: auth.PermPolicyWrite, method: http.MethodDelete, url: "/api/v1/policy/bundles/demo/shadow", pathValues: pathValues("id", "demo"), handler: (*server).handleDeletePolicyShadow},
		{name: "shadow results summary", permission: auth.PermPolicyRead, method: http.MethodGet, url: "/api/v1/policy/bundles/demo/shadow/results/summary", pathValues: pathValues("id", "demo"), handler: (*server).handleShadowResultsSummary},
		{name: "shadow results comparisons", permission: auth.PermPolicyRead, method: http.MethodGet, url: "/api/v1/policy/bundles/demo/shadow/results/comparisons", pathValues: pathValues("id", "demo"), handler: (*server).handleShadowResultsComparisons},
		{name: "shadow results timeseries", permission: auth.PermPolicyRead, method: http.MethodGet, url: "/api/v1/policy/bundles/demo/shadow/results/timeseries", pathValues: pathValues("id", "demo"), handler: (*server).handleShadowResultsTimeseries},
		{name: "create pool", permission: auth.PermPoolsWrite, method: http.MethodPut, url: "/api/v1/pools/pool-a", body: `{"display_name":"Pool A"}`, pathValues: pathValues("name", "pool-a"), handler: (*server).handleCreatePool},
		{name: "update pool", permission: auth.PermPoolsWrite, method: http.MethodPatch, url: "/api/v1/pools/pool-a", body: `{"display_name":"Pool A+"}`, pathValues: pathValues("name", "pool-a"), handler: (*server).handleUpdatePool},
		{name: "delete pool", permission: auth.PermPoolsWrite, method: http.MethodDelete, url: "/api/v1/pools/pool-a", pathValues: pathValues("name", "pool-a"), handler: (*server).handleDeletePool},
		{name: "drain pool", permission: auth.PermPoolsWrite, method: http.MethodPost, url: "/api/v1/pools/pool-a/drain", body: `{}`, pathValues: pathValues("name", "pool-a"), handler: (*server).handleDrainPool},
		{name: "add topic to pool", permission: auth.PermPoolsWrite, method: http.MethodPut, url: "/api/v1/pools/pool-a/topics/job.default", pathValues: pathValues("name", "pool-a", "topic", "job.default"), handler: (*server).handleAddTopicToPool},
		{name: "remove topic from pool", permission: auth.PermPoolsWrite, method: http.MethodDelete, url: "/api/v1/pools/pool-a/topics/job.default", pathValues: pathValues("name", "pool-a", "topic", "job.default"), handler: (*server).handleRemoveTopicFromPool},
		{name: "telemetry status", permission: auth.PermTelemetryRead, method: http.MethodGet, url: "/api/v1/telemetry/status", handler: (*server).handleGetTelemetryStatus},
		{name: "telemetry inspect", permission: auth.PermTelemetryExport, method: http.MethodGet, url: "/api/v1/telemetry/inspect", handler: (*server).handleGetTelemetryInspect},
		{name: "telemetry export", permission: auth.PermTelemetryExport, method: http.MethodGet, url: "/api/v1/telemetry/export", handler: (*server).handleGetTelemetryExport},
		{name: "telemetry usage", permission: auth.PermTelemetryRead, method: http.MethodGet, url: "/api/v1/telemetry/usage", handler: (*server).handleGetTelemetryUsage},
		{name: "telemetry consent", permission: auth.PermTelemetryWrite, method: http.MethodPost, url: "/api/v1/telemetry/consent", body: `{"mode":"off"}`, handler: (*server).handleSetTelemetryConsent},
		{name: "list topics", permission: auth.PermTopicsRead, method: http.MethodGet, url: "/api/v1/topics", handler: (*server).handleListTopics},
		{name: "create topic", permission: auth.PermTopicsWrite, method: http.MethodPost, url: "/api/v1/topics", body: `{"name":"job.rbac"}`, handler: (*server).handleCreateTopic},
		{name: "delete topic", permission: auth.PermTopicsWrite, method: http.MethodDelete, url: "/api/v1/topics/job.rbac", pathValues: pathValues("name", "job.rbac"), handler: (*server).handleDeleteTopic},
		{name: "list velocity rules", permission: auth.PermPolicyRead, method: http.MethodGet, url: "/api/v1/policy/velocity-rules", handler: (*server).handleVelocityRules},
		{name: "create velocity rule", permission: auth.PermPolicyWrite, method: http.MethodPost, url: "/api/v1/policy/velocity-rules", body: `{"id":"rbac-rule","name":"RBAC rule","window":"1m","key":"tenant","threshold":2,"decision":"deny","reason":"test"}`, handler: (*server).handleCreateVelocityRule},
		{name: "velocity rule stats", permission: auth.PermPolicyRead, method: http.MethodGet, url: "/api/v1/policy/velocity-rules/stats", handler: (*server).handleVelocityRuleStats},
		{name: "put velocity rule", permission: auth.PermPolicyWrite, method: http.MethodPut, url: "/api/v1/policy/velocity-rules/rbac-rule", body: `{"name":"RBAC rule","window":"1m","key":"tenant","threshold":2,"decision":"deny","reason":"test"}`, pathValues: pathValues("id", "rbac-rule"), handler: (*server).handlePutVelocityRule},
		{name: "delete velocity rule", permission: auth.PermPolicyWrite, method: http.MethodDelete, url: "/api/v1/policy/velocity-rules/rbac-rule", pathValues: pathValues("id", "rbac-rule"), handler: (*server).handleDeleteVelocityRule},
		{name: "list worker credentials", permission: auth.PermWorkerCredentialsRead, method: http.MethodGet, url: "/api/v1/workers/credentials", handler: (*server).handleListWorkerCredentials},
		{name: "create worker credential", permission: auth.PermWorkerCredentialsWrite, method: http.MethodPost, url: "/api/v1/workers/credentials", body: `{"worker_id":"worker-rbac"}`, handler: (*server).handleCreateWorkerCredential},
		{name: "delete worker credential", permission: auth.PermWorkerCredentialsWrite, method: http.MethodDelete, url: "/api/v1/workers/credentials/worker-rbac", pathValues: pathValues("worker_id", "worker-rbac"), handler: (*server).handleDeleteWorkerCredential},
		{name: "revoke worker session", permission: auth.PermWorkersWrite, method: http.MethodPost, url: "/api/v1/workers/worker-1/revoke-session", pathValues: pathValues("id", "worker-1"), handler: (*server).handleRevokeWorkerSession},
		{name: "mcp approval approve", permission: auth.PermJobsApprove, method: http.MethodPost, url: "/api/v1/mcp/approvals/appr-1/approve", pathValues: pathValues("id", "appr-1"), handler: (*server).handleMCPApprovalApprove},
		{name: "mcp approval reject", permission: auth.PermJobsApprove, method: http.MethodPost, url: "/api/v1/mcp/approvals/appr-1/reject", pathValues: pathValues("id", "appr-1"), handler: (*server).handleMCPApprovalReject},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newRBACPermissionServer(t)
			putTestRole(t, s, "allowed", tc.permission)
			putTestRole(t, s, "denied")

			deniedReq := buildRBACRouteRequest(tc, "denied")
			deniedRR := httptest.NewRecorder()
			tc.handler(s, deniedRR, deniedReq)
			if deniedRR.Code != http.StatusForbidden {
				t.Fatalf("missing-permission status = %d, want %d body=%s", deniedRR.Code, http.StatusForbidden, deniedRR.Body.String())
			}

			allowedReq := buildRBACRouteRequest(tc, "allowed")
			allowedRR := httptest.NewRecorder()
			tc.handler(s, allowedRR, allowedReq)
			if allowedRR.Code == http.StatusForbidden {
				t.Fatalf("allowed-permission status = %d, want != %d body=%s", allowedRR.Code, http.StatusForbidden, allowedRR.Body.String())
			}

			adminReq := buildRBACRouteRequest(tc, "admin")
			adminRR := httptest.NewRecorder()
			tc.handler(s, adminRR, adminReq)
			if adminRR.Code == http.StatusForbidden {
				t.Fatalf("admin-fallback status = %d, want != %d body=%s", adminRR.Code, http.StatusForbidden, adminRR.Body.String())
			}
		})
	}
}

func TestRBACRoutePermissions_DefaultViewerReadRoutes(t *testing.T) {
	s := newRBACPermissionServer(t)

	for _, tc := range []rbacRouteCase{
		{name: "locks read", permission: auth.PermLocksRead, method: http.MethodGet, url: "/api/v1/locks?resource=job.default", handler: (*server).handleGetLock},
		{name: "topics read", permission: auth.PermTopicsRead, method: http.MethodGet, url: "/api/v1/topics", handler: (*server).handleListTopics},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := buildRBACRouteRequest(tc, "viewer")
			rr := httptest.NewRecorder()
			tc.handler(s, rr, req)
			if rr.Code == http.StatusForbidden {
				t.Fatalf("viewer status = %d, want != %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
			}
		})
	}
}
