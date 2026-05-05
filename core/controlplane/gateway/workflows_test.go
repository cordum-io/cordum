package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/licensing"
	wf "github.com/cordum/cordum/core/workflow"
)

func TestWorkflowLifecycleHandlers(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanTeam, nil)
	s.workflowEng = wf.NewEngine(s.workflowStore, bus).
		WithMemory(s.memStore).
		WithConfig(s.configSvc).
		WithSchemaRegistry(s.schemaRegistry)

	payload := map[string]any{
		"id":     "wf-approve",
		"org_id": "default",
		"name":   "Approval Only",
		"steps": map[string]any{
			"approve": map[string]any{
				"type": "approval",
			},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleCreateWorkflow(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create workflow: %d %s", rr.Code, rr.Body.String())
	}
	var createResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &createResp)
	wfID, _ := createResp["id"].(string)
	if wfID == "" {
		t.Fatalf("workflow id missing")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	listReq.Header.Set("X-Tenant-ID", "default")
	listRR := httptest.NewRecorder()
	s.handleListWorkflows(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list workflows: %d %s", listRR.Code, listRR.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+wfID, nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	getReq.SetPathValue("id", wfID)
	getRR := httptest.NewRecorder()
	s.handleGetWorkflow(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get workflow: %d %s", getRR.Code, getRR.Body.String())
	}

	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfID+"/runs", bytes.NewReader([]byte(`{}`)))
	runReq.Header.Set("X-Tenant-ID", "default")
	runReq.SetPathValue("id", wfID)
	runRR := httptest.NewRecorder()
	s.handleStartRun(runRR, runReq)
	if runRR.Code != http.StatusOK {
		t.Fatalf("start run: %d %s", runRR.Code, runRR.Body.String())
	}
	var runResp map[string]any
	_ = json.Unmarshal(runRR.Body.Bytes(), &runResp)
	runID, _ := runResp["run_id"].(string)
	if runID == "" {
		t.Fatalf("run id missing")
	}

	runGetReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs/"+runID, nil)
	runGetReq.Header.Set("X-Tenant-ID", "default")
	runGetReq.SetPathValue("id", runID)
	runGetRR := httptest.NewRecorder()
	s.handleGetRun(runGetRR, runGetReq)
	if runGetRR.Code != http.StatusOK {
		t.Fatalf("get run: %d %s", runGetRR.Code, runGetRR.Body.String())
	}

	deleteRunReq := httptest.NewRequest(http.MethodDelete, "/api/v1/workflow-runs/"+runID, nil)
	deleteRunReq.Header.Set("X-Tenant-ID", "default")
	deleteRunReq.SetPathValue("id", runID)
	deleteRunRR := httptest.NewRecorder()
	s.handleDeleteRun(deleteRunRR, deleteRunReq)
	if deleteRunRR.Code != http.StatusNoContent {
		t.Fatalf("delete run: %d %s", deleteRunRR.Code, deleteRunRR.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/workflows/"+wfID, nil)
	deleteReq.Header.Set("X-Tenant-ID", "default")
	deleteReq.SetPathValue("id", wfID)
	deleteRR := httptest.NewRecorder()
	s.handleDeleteWorkflow(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusNoContent {
		t.Fatalf("delete workflow: %d %s", deleteRR.Code, deleteRR.Body.String())
	}
}

func TestWorkflowPolicyOverrideRoundTrip(t *testing.T) {
	s, _, _ := newTestGateway(t)
	policyOverride := validWorkflowPolicyOverride("wf-policy")
	body, _ := json.Marshal(map[string]any{
		"id":              "wf-policy",
		"org_id":          "default",
		"name":            "Workflow Policy",
		"policy_override": policyOverride,
		"steps": map[string]any{
			"start": map[string]any{"type": "approval"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleCreateWorkflow(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workflow: %d %s", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/wf-policy", nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	getReq.SetPathValue("id", "wf-policy")
	getRec := httptest.NewRecorder()
	s.handleGetWorkflow(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get workflow: %d %s", getRec.Code, getRec.Body.String())
	}
	var got wf.Workflow
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode workflow: %v", err)
	}
	if got.PolicyOverride != policyOverride {
		t.Fatalf("policy_override = %q, want %q", got.PolicyOverride, policyOverride)
	}
}

func TestCreateWorkflowRejectsInvalidPolicyOverride(t *testing.T) {
	s, _, _ := newTestGateway(t)
	body, _ := json.Marshal(map[string]any{
		"id":              "wf-invalid-policy",
		"org_id":          "default",
		"name":            "Invalid Policy",
		"policy_override": "tier: job\nselector:\n  job_id: job-1\nrules: []\n",
		"steps": map[string]any{
			"start": map[string]any{"type": "approval"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleCreateWorkflow(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid policy override, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "policy_override") {
		t.Fatalf("expected policy_override validation error, got %s", rec.Body.String())
	}
}

func TestWorkflowPolicyOverrideMergesIntoGlobalBundles(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if err := s.workflowStore.SaveWorkflow(context.Background(), &wf.Workflow{
		ID:             "wf-policy-merge",
		OrgID:          "default",
		Name:           "Workflow Policy Merge",
		PolicyOverride: validWorkflowPolicyOverride("wf-policy-merge"),
		Steps:          map[string]*wf.Step{"start": {ID: "start", Type: wf.StepTypeApproval}},
	}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	bundles, _, err := s.loadPolicyBundles(context.Background())
	if err != nil {
		t.Fatalf("load policy bundles: %v", err)
	}
	if _, ok := bundles["workflow/wf-policy-merge/policy"]; !ok {
		t.Fatalf("missing workflow policy override bundle: %+v", bundles)
	}
	policy, snapshot, err := policybundles.BuildPolicyFromBundles(bundles)
	if err != nil {
		t.Fatalf("BuildPolicyFromBundles: %v", err)
	}
	if !strings.HasPrefix(snapshot, "cfg:") {
		t.Fatalf("snapshot = %q, want cfg: prefix", snapshot)
	}
	if policy == nil || len(policy.Rules) != 1 {
		t.Fatalf("expected one merged workflow rule, got %+v", policy)
	}
	rule := policy.Rules[0]
	if rule.Tier != "workflow" || rule.Selector.WorkflowID != "wf-policy-merge" {
		t.Fatalf("merged rule tier/selector = %q/%+v, want workflow/wf-policy-merge", rule.Tier, rule.Selector)
	}

	if err := s.workflowStore.DeleteWorkflow(context.Background(), "wf-policy-merge"); err != nil {
		t.Fatalf("delete workflow: %v", err)
	}
	bundles, _, err = s.loadPolicyBundles(context.Background())
	if err != nil {
		t.Fatalf("reload policy bundles: %v", err)
	}
	if _, ok := bundles["workflow/wf-policy-merge/policy"]; ok {
		t.Fatalf("workflow policy override bundle still present after delete: %+v", bundles)
	}
}

func validWorkflowPolicyOverride(workflowID string) string {
	return "tier: workflow\nselector:\n  workflow_id: " + workflowID + "\ndefault_decision: deny\nrules:\n  - id: workflow-deny-deploy\n    decision: deny\n    match:\n      topics: [\"job.deploy\"]\n"
}

func TestCreateWorkflowRejectsInvalidStepID(t *testing.T) {
	s, _, _ := newTestGateway(t)
	payload := map[string]any{
		"id":     "wf-invalid-step",
		"org_id": "default",
		"name":   "Invalid Step",
		"steps": map[string]any{
			"bad/step": map[string]any{
				"type": "approval",
			},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleCreateWorkflow(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid step id, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "workflow step id") {
		t.Fatalf("expected step id validation error, got %s", rr.Body.String())
	}
}
