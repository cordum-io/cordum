package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
	wf "github.com/cordum/cordum/core/workflow"
)

func assertCodedError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, status, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v; body=%s", err, rec.Body.String())
	}
	if got, _ := body["code"].(string); got != code {
		t.Fatalf("code = %q, want %q; body=%v", got, code, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("error message missing from body=%v", body)
	}
}

func submitJobForCode(t *testing.T, s *server, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleSubmitJobHTTP(rec, req)
	return rec
}

func TestHandleJobSubmit_IdempotencyConflictReturnsCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"

	const idemKey = "submit-conflict-empty-owner"
	if err := s.jobStore.Client().
		Set(context.Background(), "job:idempotency:default:"+idemKey, "", time.Minute).
		Err(); err != nil {
		t.Fatalf("seed idempotency conflict: %v", err)
	}

	rec := submitJobForCode(t, s, map[string]any{
		"prompt":          "hello",
		"topic":           "job.test",
		"idempotency_key": idemKey,
	})

	assertCodedError(t, rec, http.StatusConflict, "IDEMPOTENCY_CONFLICT")
}

func TestHandleJobSubmit_BackpressureReturnsCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	ctx := context.Background()
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"rate_limits": map[string]any{
				"concurrent_jobs": 1,
				"queue_size":      0,
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}
	if err := s.jobStore.SetTenant(ctx, "job-seed", "default"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if err := s.jobStore.SetState(ctx, "job-seed", model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	rec := submitJobForCode(t, s, map[string]any{"prompt": "hello", "topic": "job.test"})

	assertCodedError(t, rec, http.StatusTooManyRequests, "BACKPRESSURE")
}

func TestHandleJobSubmit_MemoryPolicyViolationReturnsCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"context": map[string]any{
				"allowed_memory_ids": []string{"repo:*"},
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	rec := submitJobForCode(t, s, map[string]any{
		"prompt":    "hello",
		"topic":     "job.test",
		"memory_id": "kb:secret",
	})

	assertCodedError(t, rec, http.StatusForbidden, "MEMORY_POLICY_VIOLATION")
}

func seedWorkflowRunForCode(t *testing.T, s *server, runID string, status wf.RunStatus) *wf.Workflow {
	t.Helper()
	workflow := &wf.Workflow{
		ID:    "wf-" + runID,
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), workflow); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &wf.WorkflowRun{
		ID:         runID,
		WorkflowID: workflow.ID,
		OrgID:      "default",
		Status:     status,
		Steps: map[string]*wf.StepRun{
			"step": {StepID: "step", Status: wf.StepStatusRunning},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return workflow
}

func TestHandleRunStart_NotRunnableReturnsCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"limits": map[string]any{"max_concurrent_runs": 1},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}
	workflow := seedWorkflowRunForCode(t, s, "run-blocking-coded", wf.RunStatusRunning)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+workflow.ID+"/runs", strings.NewReader(`{}`))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", workflow.ID)
	rec := httptest.NewRecorder()
	s.handleStartRun(rec, req)

	assertCodedError(t, rec, http.StatusTooManyRequests, "RUN_NOT_RUNNABLE")
}

func TestHandleRunStart_IdempotencyConflictReturnsCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	workflow := seedWorkflowRunForCode(t, s, "run-idem-holder", wf.RunStatusSucceeded)
	const idemKey = "run-start-empty-owner"
	if err := s.jobStore.Client().
		Set(context.Background(), "wf:run:idempotency:"+idemKey, "", 0).
		Err(); err != nil {
		t.Fatalf("seed run idempotency conflict: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+workflow.ID+"/runs", strings.NewReader(`{}`))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Idempotency-Key", idemKey)
	req.SetPathValue("id", workflow.ID)
	rec := httptest.NewRecorder()
	s.handleStartRun(rec, req)

	assertCodedError(t, rec, http.StatusConflict, "RUN_IDEMPOTENCY_CONFLICT")
}

func TestHandleRunCancel_NotCancellableReturnsCode(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.workflowEng = wf.NewEngine(s.workflowStore, bus).
		WithMemory(s.memStore).
		WithConfig(s.configSvc).
		WithSchemaRegistry(s.schemaRegistry)
	seedWorkflowRunForCode(t, s, "run-cancel-locked", wf.RunStatusRunning)
	lockKey := "cordum:wf:run:lock:run-cancel-locked"
	token, err := s.jobStore.TryAcquireLock(context.Background(), lockKey, time.Minute)
	if err != nil || token == "" {
		t.Fatalf("acquire run lock token=%q err=%v", token, err)
	}
	defer func() {
		_ = s.jobStore.ReleaseLock(context.Background(), lockKey, token)
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow-runs/run-cancel-locked/cancel", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("run_id", "run-cancel-locked")
	rec := httptest.NewRecorder()
	s.handleCancelRun(rec, req)

	assertCodedError(t, rec, http.StatusConflict, "RUN_NOT_CANCELLABLE")
}

func TestHandleRunRerun_NotRunnableReturnsCode(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.workflowEng = wf.NewEngine(s.workflowStore, bus).
		WithMemory(s.memStore).
		WithConfig(s.configSvc).
		WithSchemaRegistry(s.schemaRegistry)
	seedWorkflowRunForCode(t, s, "run-rerun-coded", wf.RunStatusSucceeded)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow-runs/run-rerun-coded/rerun",
		strings.NewReader(`{"from_step":"missing-step"}`))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", "run-rerun-coded")
	rec := httptest.NewRecorder()
	s.handleRerunRun(rec, req)

	assertCodedError(t, rec, http.StatusBadRequest, "RUN_NOT_RUNNABLE")
}

func TestHandleApprovalResult_InvalidStatusReturnsCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/missing-job/approve", strings.NewReader(`{}`))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("job_id", "missing-job")
	req = withAuth(req, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	rec := httptest.NewRecorder()

	s.handleApproveJob(rec, req)

	assertCodedError(t, rec, http.StatusNotFound, "approval_not_found")
}
