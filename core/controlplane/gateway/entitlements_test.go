package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/licensing"
	wf "github.com/cordum/cordum/core/workflow"
)

func TestHandleSubmitJobHTTP_PromptTierLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.MaxPromptChars = 5
	})

	body := bytes.NewBufferString(`{"prompt":"abcdef","topic":"job.test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", body)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"limit":"max_prompt_chars"`) {
		t.Fatalf("expected max_prompt_chars limit error, got %s", rec.Body.String())
	}
}

func TestHandleSubmitJobHTTP_BodyTierLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.MaxBodyBytes = 32
	})

	body := bytes.NewBufferString(`{"prompt":"this body is intentionally too large","topic":"job.test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", body)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"limit":"max_body_bytes"`) {
		t.Fatalf("expected max_body_bytes limit error, got %s", rec.Body.String())
	}
}

func TestHandleCreateWorkerCredential_MaxWorkersLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanCommunity, nil)

	for _, workerID := range []string{"worker-1", "worker-2", "worker-3"} {
		req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", strings.NewReader(`{"worker_id":"`+workerID+`"}`)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.handleCreateWorkerCredential(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected worker credential create for %s to succeed, got %d: %s", workerID, rec.Code, rec.Body.String())
		}
	}

	limitReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", strings.NewReader(`{"worker_id":"worker-4"}`)))
	limitReq.Header.Set("Content-Type", "application/json")
	limitRec := httptest.NewRecorder()
	s.handleCreateWorkerCredential(limitRec, limitReq)

	if limitRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", limitRec.Code, limitRec.Body.String())
	}
	if !strings.Contains(limitRec.Body.String(), `"limit":"max_workers"`) {
		t.Fatalf("expected max_workers limit error, got %s", limitRec.Body.String())
	}
}

func TestHandleCreateWorkflow_MaxWorkflowStepsLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.MaxWorkflowSteps = 1
	})

	body := bytes.NewBufferString(`{
		"id":"wf-too-many-steps",
		"org_id":"default",
		"name":"Too many steps",
		"steps":{
			"s1":{"type":"delay","delay_sec":1},
			"s2":{"type":"delay","delay_sec":1}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleCreateWorkflow(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"limit":"max_workflow_steps"`) {
		t.Fatalf("expected max_workflow_steps limit error, got %s", rec.Body.String())
	}
}

func TestHandleStartRun_MaxActiveWorkflowsLimit(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.MaxActiveWorkflows = 1
	})
	s.workflowEng = wf.NewEngine(s.workflowStore, bus).
		WithMemory(s.memStore).
		WithConfig(s.configSvc).
		WithSchemaRegistry(s.schemaRegistry)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(`{
		"id":"wf-active-limit",
		"org_id":"default",
		"name":"Active limit",
		"steps":{"approve":{"type":"approval"}}
	}`))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-Tenant-ID", "default")
	createRec := httptest.NewRecorder()
	s.handleCreateWorkflow(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create workflow: %d %s", createRec.Code, createRec.Body.String())
	}

	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf-active-limit/runs", strings.NewReader(`{}`))
	startReq.SetPathValue("id", "wf-active-limit")
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("X-Tenant-ID", "default")
	startRec := httptest.NewRecorder()
	s.handleStartRun(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("first start run: %d %s", startRec.Code, startRec.Body.String())
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf-active-limit/runs", strings.NewReader(`{}`))
	secondReq.SetPathValue("id", "wf-active-limit")
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.Header.Set("X-Tenant-ID", "default")
	secondRec := httptest.NewRecorder()
	s.handleStartRun(secondRec, secondReq)

	if secondRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", secondRec.Code, secondRec.Body.String())
	}
	if !strings.Contains(secondRec.Body.String(), `"limit":"max_active_workflows"`) {
		t.Fatalf("expected max_active_workflows limit error, got %s", secondRec.Body.String())
	}
}

func TestHandleRegisterSchema_MaxSchemaCountLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.MaxSchemaCount = 1
	})

	firstReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/config/schemas", strings.NewReader(`{"id":"schema-1","schema":{"type":"object"}}`)))
	firstReq.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	s.handleRegisterSchema(firstRec, firstReq)
	if firstRec.Code != http.StatusNoContent {
		t.Fatalf("first schema register: %d %s", firstRec.Code, firstRec.Body.String())
	}

	secondReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/config/schemas", strings.NewReader(`{"id":"schema-2","schema":{"type":"object"}}`)))
	secondReq.Header.Set("Content-Type", "application/json")
	secondRec := httptest.NewRecorder()
	s.handleRegisterSchema(secondRec, secondReq)

	if secondRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", secondRec.Code, secondRec.Body.String())
	}
	if !strings.Contains(secondRec.Body.String(), `"limit":"max_schema_count"`) {
		t.Fatalf("expected max_schema_count limit error, got %s", secondRec.Body.String())
	}
}

func TestHandlePutPolicyBundle_MaxPolicyBundlesLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.MaxPolicyBundles = 1
	})

	firstBody, _ := json.Marshal(map[string]any{"content": policyContent, "enabled": true})
	firstReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/one", bytes.NewReader(firstBody))
	firstReq.Header.Set("X-Tenant-ID", "default")
	firstReq.Header.Set("X-Principal-Role", "admin")
	firstReq.SetPathValue("id", "secops/one")
	firstRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first policy bundle put: %d %s", firstRec.Code, firstRec.Body.String())
	}

	secondBody, _ := json.Marshal(map[string]any{"content": policyContent, "enabled": true})
	secondReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/two", bytes.NewReader(secondBody))
	secondReq.Header.Set("X-Tenant-ID", "default")
	secondReq.Header.Set("X-Principal-Role", "admin")
	secondReq.SetPathValue("id", "secops/two")
	secondRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(secondRec, secondReq)

	if secondRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", secondRec.Code, secondRec.Body.String())
	}
	if !strings.Contains(secondRec.Body.String(), `"limit":"max_policy_bundles"`) {
		t.Fatalf("expected max_policy_bundles limit error, got %s", secondRec.Body.String())
	}
}

func TestLicenseEndpointsReturnPlanRightsAndUsage(t *testing.T) {
	s, _, _ := newTestGateway(t)
	claims := licensing.Claims{
		Plan: string(licensing.PlanTeam),
		Rights: &licensing.Rights{
			HostedService: true,
			WhiteLabel:    true,
		},
		Entitlements: &licensing.Entitlements{
			ApprovalMode:       string(licensing.ApprovalModeCustom),
			MaxWorkers:         30,
			MaxConcurrentJobs:  27,
			MaxWorkflowSteps:   9,
			MaxActiveWorkflows: 11,
			MaxSchemaCount:     13,
			MaxPromptChars:     17,
			MaxBodyBytes:       128,
			MaxPolicyBundles:   19,
			RequestsPerSecond:  2300,
		},
	}
	setTestLicense(t, s, claims)

	workerReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", strings.NewReader(`{"worker_id":"worker-usage"}`)))
	workerReq.Header.Set("Content-Type", "application/json")
	workerRec := httptest.NewRecorder()
	s.handleCreateWorkerCredential(workerRec, workerReq)
	if workerRec.Code != http.StatusCreated {
		t.Fatalf("seed worker credential: %d %s", workerRec.Code, workerRec.Body.String())
	}

	schemaReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/config/schemas", strings.NewReader(`{"id":"schema-usage","schema":{"type":"object"}}`)))
	schemaReq.Header.Set("Content-Type", "application/json")
	schemaRec := httptest.NewRecorder()
	s.handleRegisterSchema(schemaRec, schemaReq)
	if schemaRec.Code != http.StatusNoContent {
		t.Fatalf("seed schema: %d %s", schemaRec.Code, schemaRec.Body.String())
	}

	bundleBody, _ := json.Marshal(map[string]any{"content": policyContent, "enabled": true})
	bundleReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/usage", bytes.NewReader(bundleBody))
	bundleReq.Header.Set("X-Tenant-ID", "default")
	bundleReq.Header.Set("X-Principal-Role", "admin")
	bundleReq.SetPathValue("id", "secops/usage")
	bundleRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(bundleRec, bundleReq)
	if bundleRec.Code != http.StatusOK {
		t.Fatalf("seed policy bundle: %d %s", bundleRec.Code, bundleRec.Body.String())
	}

	licenseReq := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/license", nil))
	licenseReq.Header.Set("X-Tenant-ID", "default")
	licenseRec := httptest.NewRecorder()
	s.handleGetLicense(licenseRec, licenseReq)
	if licenseRec.Code != http.StatusOK {
		t.Fatalf("get license: %d %s", licenseRec.Code, licenseRec.Body.String())
	}

	var licenseResp map[string]any
	if err := json.NewDecoder(licenseRec.Body).Decode(&licenseResp); err != nil {
		t.Fatalf("decode license response: %v", err)
	}
	if got := licenseResp["plan"]; got != string(licensing.PlanTeam) {
		t.Fatalf("plan = %v, want %s", got, licensing.PlanTeam)
	}
	if _, ok := licenseResp["rights"]; !ok {
		t.Fatalf("expected rights in license response")
	}
	if _, ok := licenseResp["entitlements"]; !ok {
		t.Fatalf("expected entitlements in license response")
	}
	if _, ok := licenseResp["license"]; !ok {
		t.Fatalf("expected license info in license response")
	}
	if strings.TrimSpace(anyString(licenseResp["expiry_status"])) == "" {
		t.Fatalf("expected expiry_status in license response")
	}

	usageReq := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/license/usage", nil))
	usageReq.Header.Set("X-Tenant-ID", "default")
	usageRec := httptest.NewRecorder()
	s.handleGetLicenseUsage(usageRec, usageReq)
	if usageRec.Code != http.StatusOK {
		t.Fatalf("get license usage: %d %s", usageRec.Code, usageRec.Body.String())
	}

	var usageResp map[string]any
	if err := json.NewDecoder(usageRec.Body).Decode(&usageResp); err != nil {
		t.Fatalf("decode license usage response: %v", err)
	}
	usage, ok := usageResp["usage"].(map[string]any)
	if !ok {
		t.Fatalf("expected usage map, got %#v", usageResp["usage"])
	}
	expectAllowed := func(key string, want float64) {
		t.Helper()
		item, ok := usage[key].(map[string]any)
		if !ok {
			t.Fatalf("expected usage[%s] map, got %#v", key, usage[key])
		}
		if got := item["allowed"]; got != want {
			t.Fatalf("usage[%s].allowed = %#v, want %v", key, got, want)
		}
	}
	expectAllowed("workers", 30)
	expectAllowed("concurrent_jobs", 27)
	expectAllowed("active_workflows", 11)
	expectAllowed("workflow_steps", 9)
	expectAllowed("schemas", 13)
	expectAllowed("policy_bundles", 19)
	expectAllowed("requests_per_second", 2300)
	expectAllowed("prompt_chars", 17)
	expectAllowed("body_bytes", 128)
	approvalMode, ok := usage["approval_mode"].(map[string]any)
	if !ok || approvalMode["allowed"] != string(licensing.ApprovalModeCustom) {
		t.Fatalf("unexpected approval_mode usage payload: %#v", usage["approval_mode"])
	}
	workers, ok := usage["workers"].(map[string]any)
	if !ok || workers["current"] != float64(1) {
		t.Fatalf("unexpected workers usage payload: %#v", usage["workers"])
	}
	schemas, ok := usage["schemas"].(map[string]any)
	if !ok || schemas["current"] != float64(1) {
		t.Fatalf("unexpected schemas usage payload: %#v", usage["schemas"])
	}
	policyBundles, ok := usage["policy_bundles"].(map[string]any)
	if !ok || policyBundles["current"] != float64(1) {
		t.Fatalf("unexpected policy_bundles usage payload: %#v", usage["policy_bundles"])
	}
}
