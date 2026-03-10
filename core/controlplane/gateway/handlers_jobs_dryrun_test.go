package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

func TestHandleWorkflowDryRun(t *testing.T) {
	s, _, safety := newTestGateway(t)

	// Create a test workflow with 2 job steps and 1 delay step.
	wfDef := &wf.Workflow{
		ID:    "wf-dryrun-test",
		OrgID: "default",
		Name:  "Dry Run Test",
		Steps: map[string]*wf.Step{
			"fetch-data": {ID: "fetch-data", Name: "Fetch Data", Type: wf.StepTypeLLM, Topic: "job.llm.fetch"},
			"deploy":     {ID: "deploy", Name: "Deploy", Type: wf.StepTypeWorker, Topic: "job.deploy.prod"},
			"wait":       {ID: "wait", Name: "Wait 30s", Type: wf.StepTypeDelay},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	// Configure safety client to return ALLOW for all steps.
	safety.setResponse(&pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:   "test allow",
		RuleId:   "rule-allow",
	})

	body, _ := json.Marshal(dryRunRequest{
		Input:       map[string]any{"key": "value"},
		Environment: "staging",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf-dryrun-test/dry-run", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", "wf-dryrun-test")
	rec := httptest.NewRecorder()
	s.handleWorkflowDryRun(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp dryRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.WorkflowID != "wf-dryrun-test" {
		t.Fatalf("expected workflow_id wf-dryrun-test, got %s", resp.WorkflowID)
	}
	if len(resp.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(resp.Steps))
	}

	// Build a lookup by step_id for deterministic assertions.
	stepMap := make(map[string]dryRunStepResult)
	for _, s := range resp.Steps {
		stepMap[s.StepID] = s
	}

	// Job steps should have ALLOW decision.
	for _, stepID := range []string{"fetch-data", "deploy"} {
		s, ok := stepMap[stepID]
		if !ok {
			t.Fatalf("step %s not found in response", stepID)
		}
		if s.Decision != "ALLOW" {
			t.Errorf("step %s: expected ALLOW, got %s", stepID, s.Decision)
		}
		if s.RuleID != "rule-allow" {
			t.Errorf("step %s: expected rule_id rule-allow, got %s", stepID, s.RuleID)
		}
	}

	// Delay step should be N/A.
	waitStep, ok := stepMap["wait"]
	if !ok {
		t.Fatalf("step wait not found in response")
	}
	if waitStep.Decision != "N/A" {
		t.Errorf("step wait: expected N/A, got %s", waitStep.Decision)
	}
	if waitStep.Reason != "non-job step" {
		t.Errorf("step wait: expected reason 'non-job step', got %s", waitStep.Reason)
	}
}

func TestHandleWorkflowDryRunRequireApproval(t *testing.T) {
	s, _, safety := newTestGateway(t)

	wfDef := &wf.Workflow{
		ID:    "wf-dryrun-approval",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"risky": {ID: "risky", Name: "Risky Step", Type: wf.StepTypeHTTP, Topic: "job.http.prod"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	safety.setResponse(&pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:   "production deployment requires approval",
		RuleId:   "rule-prod",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf-dryrun-approval/dry-run", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", "wf-dryrun-approval")
	rec := httptest.NewRecorder()
	s.handleWorkflowDryRun(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp dryRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(resp.Steps))
	}
	if resp.Steps[0].Decision != "REQUIRE_APPROVAL" {
		t.Errorf("expected REQUIRE_APPROVAL, got %s", resp.Steps[0].Decision)
	}
}

func TestHandleWorkflowDryRunNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/nonexistent/dry-run", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	s.handleWorkflowDryRun(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkflowDryRunForbiddenWithoutAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Set auth provider so requireRole actually checks.
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"viewer-key","role":"viewer"}]`,
	})
	s.auth = provider

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf-test/dry-run", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Api-Key", "viewer-key")
	req.SetPathValue("id", "wf-test")

	// Inject auth context with viewer role (not admin).
	authCtx := &AuthContext{Role: "viewer", Tenant: "default"}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))

	rec := httptest.NewRecorder()
	s.handleWorkflowDryRun(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkflowDryRunSanitizesErrors(t *testing.T) {
	s, _, safety := newTestGateway(t)

	wfDef := &wf.Workflow{
		ID:    "wf-dryrun-err",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step-a": {ID: "step-a", Name: "Step A", Type: wf.StepTypeLLM, Topic: "job.llm.test"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	// Inject an error into the safety client's Simulate method.
	safety.mu.Lock()
	safety.simulateErr = fmt.Errorf("connection refused: dial tcp 10.0.0.5:50051: i/o timeout")
	safety.mu.Unlock()

	body, _ := json.Marshal(dryRunRequest{Input: map[string]any{"x": 1}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf-dryrun-err/dry-run", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", "wf-dryrun-err")
	rec := httptest.NewRecorder()
	s.handleWorkflowDryRun(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp dryRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(resp.Steps))
	}

	step := resp.Steps[0]
	if step.Decision != "ERROR" {
		t.Errorf("expected decision ERROR, got %s", step.Decision)
	}
	if step.Reason != "safety evaluation unavailable" {
		t.Errorf("expected sanitized reason, got %q", step.Reason)
	}
	// Must NOT contain internal error details.
	if strings.Contains(step.Reason, "connection refused") || strings.Contains(step.Reason, "i/o timeout") {
		t.Errorf("reason leaks internal error: %q", step.Reason)
	}
}
