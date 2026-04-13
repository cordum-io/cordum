package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupApprovalJob creates a job in APPROVAL state with the given safety decision.
func setupApprovalJob(t *testing.T, s *server, jobID, tenant string, sd model.SafetyDecisionRecord) {
	t.Helper()
	ctx := context.Background()

	req := &pb.JobRequest{
		JobId: jobID,
		Topic: "test.topic",
		Labels: map[string]string{
			"tenant_id": tenant,
		},
	}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("SetJobMeta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("SetJobRequest: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(ctx, jobID, sd); err != nil {
		t.Fatalf("SetSafetyDecision: %v", err)
	}
	if tenant != "" {
		if err := s.jobStore.SetTenant(ctx, jobID, tenant); err != nil {
			t.Fatalf("SetTenant: %v", err)
		}
	}
}

// setupApprovalJobWithHash creates an approval job and sets the correct hash
// so the approve handler can validate it.
func setupApprovalJobWithHash(t *testing.T, s *server, jobID, tenant string) {
	t.Helper()
	ctx := context.Background()

	sd := model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-test",
	}
	setupApprovalJob(t, s, jobID, tenant, sd)

	// Recompute the hash from the stored request so it matches at approve time.
	jobReq, err := s.jobStore.GetJobRequest(ctx, jobID)
	require.NoError(t, err)
	hash, err := scheduler.HashJobRequest(jobReq)
	require.NoError(t, err)
	sd.JobHash = hash
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, sd))
}

func TestListApprovalsBasic(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Create two jobs in APPROVAL state.
	for _, id := range []string{"job-list-1", "job-list-2"} {
		setupApprovalJob(t, s, id, "", model.SafetyDecisionRecord{
			Decision:         model.SafetyRequireApproval,
			ApprovalRequired: true,
			PolicySnapshot:   "snap-test",
			JobHash:          "hash-" + id,
		})
	}

	req := httptest.NewRequest("GET", "/api/v1/approvals?include_resolved=false", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	assert.Len(t, items, 2, "should list both approval items")

	// Verify each item has safety decision fields populated.
	for _, raw := range items {
		item := raw.(map[string]any)
		assert.NotNil(t, item["job"], "each item should have a job field")
		assert.NotEmpty(t, item["decision"], "each item should have a decision")
		assert.Equal(t, "pending", item["approval_status"])
		assert.Equal(t, "actionable", item["approval_actionability"])
		assert.Equal(t, float64(1), item["approval_revision"])
	}
}

func TestListApprovalsEmptySafetyDecision(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Create a job with an empty safety decision (all zero values).
	setupApprovalJob(t, s, "job-empty-sd", "", model.SafetyDecisionRecord{})

	req := httptest.NewRequest("GET", "/api/v1/approvals?include_resolved=false", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	// Item should still be included with empty/zero fields (not corrupted).
	assert.Len(t, items, 1, "item with empty safety decision should still be listed")
}

func TestListApprovalsIncludesWorkflowApprovalJobInput(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-workflow-context"
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	payload := []byte(`{"kind":"workflow_approval_context","version":1,"workflow":{"workflow_id":"wf-1","run_id":"run-1","step_id":"approve","step_name":"Manager Approval"},"decision":{"amount":1250,"currency":"USD","vendor":"Acme Travel","escalation_reason":"manager threshold exceeded"}}`)
	require.NoError(t, s.memStore.PutContext(ctx, ctxKey, payload))

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      capsdk.SubjectWorkflowApprovalGate,
		ContextPtr: ctxPtr,
		Labels: map[string]string{
			"workflow_id": "wf-1",
			"run_id":      "run-1",
			"step_id":     "approve",
			"gate_type":   "workflow_approval",
		},
	}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, req))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, req))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		JobHash:          "hash-" + jobID,
	}))

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals?include_resolved=false", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, httpReq)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	require.Len(t, items, 1)

	item := items[0].(map[string]any)
	assert.Equal(t, ctxPtr, item["context_ptr"])
	assert.Equal(t, "wf-1", item["workflow_id"])
	assert.Equal(t, "run-1", item["workflow_run_id"])

	jobInput, ok := item["job_input"].(map[string]any)
	require.True(t, ok, "expected job_input map")
	decision, ok := jobInput["decision"].(map[string]any)
	require.True(t, ok, "expected decision map")
	assert.Equal(t, "Acme Travel", decision["vendor"])

	summary, ok := item["decision_summary"].(map[string]any)
	require.True(t, ok, "expected decision_summary map")
	assert.Equal(t, "workflow_payload", summary["source"])
	assert.Equal(t, "rich", summary["completeness"])
	assert.Equal(t, "available", summary["context_status"])
	assert.Equal(t, "Acme Travel", summary["vendor"])
	assert.Equal(t, "manager threshold exceeded", summary["why"])
	assert.Contains(t, summary["title"], "Acme Travel")
}

func TestApproveJobWithTraceIDError(t *testing.T) {
	s, _, sc := newTestGateway(t)
	sc.setSnapshots([]string{"snap-test"})

	jobID := "job-trace-err"
	setupApprovalJobWithHash(t, s, jobID, "")

	// Do NOT set a trace ID — GetTraceID will return redis.Nil.
	// The approval should still succeed with an empty trace ID.
	body := `{"reason":"test approve"}`
	req := httptest.NewRequest("POST", "/api/v1/jobs/"+jobID+"/approve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("job_id", jobID)
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "approval should succeed even when trace ID lookup fails; body: %s", rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, jobID, resp["job_id"])
	// trace_id should be empty string (not nil, not error).
	assert.Equal(t, "", resp["trace_id"], "trace_id should be empty when lookup fails")
}

func TestApproveWorkflowGateByTopicWithoutGateLabel(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-workflow-topic-only"
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	require.NoError(t, s.memStore.PutContext(ctx, ctxKey, []byte(`{"kind":"workflow_approval_context","version":1,"workflow":{"workflow_id":"wf-1","run_id":"run-1","step_id":"approve"}}`)))

	jobReq := &pb.JobRequest{
		JobId:      jobID,
		Topic:      capsdk.SubjectWorkflowApprovalGate,
		ContextPtr: ctxPtr,
	}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, jobReq))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, jobReq))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	hash, err := scheduler.HashJobRequest(jobReq)
	require.NoError(t, err)
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		JobHash:          hash,
	}))

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+jobID+"/approve", strings.NewReader(`{"reason":"ok"}`))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.SetPathValue("job_id", jobID)
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, httpReq)

	require.Equal(t, http.StatusOK, rr.Code, "workflow gate approval should succeed when identified by topic only; body: %s", rr.Body.String())
}

func TestListApprovalsDecisionSummaryMarksMalformedContext(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-workflow-malformed-context"
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	require.NoError(t, s.memStore.PutContext(ctx, ctxKey, []byte(`{"broken":`)))

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      capsdk.SubjectWorkflowApprovalGate,
		ContextPtr: ctxPtr,
		Labels: map[string]string{
			"workflow_id": "wf-2",
			"run_id":      "run-2",
			"step_id":     "manager-approval",
			"gate_type":   "workflow_approval",
		},
	}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, req))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, req))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		Reason:           "manager review required",
		JobHash:          "hash-" + jobID,
	}))

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals?include_resolved=false", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, httpReq)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	require.Len(t, items, 1)

	summary, ok := items[0].(map[string]any)["decision_summary"].(map[string]any)
	require.True(t, ok, "expected decision_summary map")
	assert.Equal(t, "workflow_payload", summary["source"])
	assert.Equal(t, "malformed", summary["context_status"])
	assert.Equal(t, "partial", summary["completeness"])
	assert.Equal(t, "manager review required", summary["why"])
	assert.Contains(t, summary["missing_fields"].([]any), "approval_context")
	assert.Contains(t, summary["missing_fields"].([]any), "business_context")
}

func TestListApprovalsDecisionSummaryMarksPartialAvailableContext(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-workflow-partial-context"
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	payload := []byte(`{"kind":"workflow_approval_context","version":1,"workflow":{"workflow_id":"wf-3","run_id":"run-3","step_id":"legal-review","step_name":"Legal Review"},"decision":{"approval_reason":"legal sign-off required"}}`)
	require.NoError(t, s.memStore.PutContext(ctx, ctxKey, payload))

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      capsdk.SubjectWorkflowApprovalGate,
		ContextPtr: ctxPtr,
		Labels: map[string]string{
			"workflow_id": "wf-3",
			"run_id":      "run-3",
			"step_id":     "legal-review",
			"gate_type":   "workflow_approval",
		},
	}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, req))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, req))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		Reason:           "fallback reason should not win",
		JobHash:          "hash-" + jobID,
	}))

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals?include_resolved=false", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, httpReq)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	require.Len(t, items, 1)

	summary, ok := items[0].(map[string]any)["decision_summary"].(map[string]any)
	require.True(t, ok, "expected decision_summary map")
	assert.Equal(t, "workflow_payload", summary["source"])
	assert.Equal(t, "available", summary["context_status"])
	assert.Equal(t, "partial", summary["completeness"])
	assert.Equal(t, "legal sign-off required", summary["why"])
	assert.Equal(t, "Approve Legal Review", summary["title"])

	missingFields, ok := summary["missing_fields"].([]any)
	require.True(t, ok, "expected missing_fields array")
	assert.Contains(t, missingFields, "business_context")
	assert.NotContains(t, missingFields, "approval_context")
	assert.NotContains(t, missingFields, "why")
}

func TestListApprovalsDecisionSummaryMarksMissingContext(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-workflow-missing-context"
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      capsdk.SubjectWorkflowApprovalGate,
		ContextPtr: ctxPtr,
		Labels: map[string]string{
			"workflow_id": "wf-4",
			"run_id":      "run-4",
			"step_id":     "manager-approval",
			"gate_type":   "workflow_approval",
		},
	}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, req))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, req))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		Reason:           "manager review required",
		JobHash:          "hash-" + jobID,
	}))

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals?include_resolved=false", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, httpReq)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	require.Len(t, items, 1)

	item := items[0].(map[string]any)
	assert.Equal(t, ctxPtr, item["context_ptr"])
	_, hasJobInput := item["job_input"]
	assert.False(t, hasJobInput, "missing context should not synthesize job_input")

	summary, ok := item["decision_summary"].(map[string]any)
	require.True(t, ok, "expected decision_summary map")
	assert.Equal(t, "workflow_payload", summary["source"])
	assert.Equal(t, "missing", summary["context_status"])
	assert.Equal(t, "partial", summary["completeness"])
	assert.Equal(t, "manager review required", summary["why"])
	assert.Equal(t, "Approve manager-approval", summary["title"])
	assert.Contains(t, summary["missing_fields"].([]any), "approval_context")
	assert.Contains(t, summary["missing_fields"].([]any), "business_context")
}

func TestListApprovalsDecisionSummaryMarksUnavailableWhenMemoryStoreMissing(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	s.memStore = nil

	jobID := "job-workflow-unavailable-context"
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      capsdk.SubjectWorkflowApprovalGate,
		ContextPtr: ctxPtr,
		Labels: map[string]string{
			"workflow_id": "wf-5",
			"run_id":      "run-5",
			"step_id":     "director-approval",
			"gate_type":   "workflow_approval",
		},
	}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, req))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, req))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		Reason:           "director sign-off required",
		JobHash:          "hash-" + jobID,
	}))

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals?include_resolved=false", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, httpReq)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	require.Len(t, items, 1)

	item := items[0].(map[string]any)
	assert.Equal(t, ctxPtr, item["context_ptr"])
	_, hasJobInput := item["job_input"]
	assert.False(t, hasJobInput, "unavailable context should not expose a blank job_input")

	summary, ok := item["decision_summary"].(map[string]any)
	require.True(t, ok, "expected decision_summary map")
	assert.Equal(t, "workflow_payload", summary["source"])
	assert.Equal(t, "unavailable", summary["context_status"])
	assert.Equal(t, "partial", summary["completeness"])
	assert.Equal(t, "director sign-off required", summary["why"])
	assert.Contains(t, summary["missing_fields"].([]any), "approval_context")
	assert.Contains(t, summary["missing_fields"].([]any), "business_context")
}

func TestListApprovalsDecisionSummaryFallsBackForLegacyApprovals(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-legacy-approval-summary"
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.finance.expense.review",
		TenantId: "default",
	}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, req))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, req))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		Reason:           "finance review required",
		PolicySnapshot:   "snap-legacy",
		JobHash:          "hash-" + jobID,
	}))

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals?include_resolved=false", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, httpReq)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	require.Len(t, items, 1)

	item := items[0].(map[string]any)
	summary, ok := item["decision_summary"].(map[string]any)
	require.True(t, ok, "expected decision_summary map")
	assert.Equal(t, "policy_only", summary["source"])
	assert.Equal(t, "minimal", summary["completeness"])
	assert.Equal(t, "absent", summary["context_status"])
	assert.Equal(t, "finance review required", summary["why"])
	assert.Equal(t, "Review job.finance.expense.review", summary["title"])
	_, hasJobInput := item["job_input"]
	assert.False(t, hasJobInput, "legacy approvals should not require synthetic job_input blobs")
}

func TestListResolvedDeniedWorkflowApprovalsRetainDecisionSummaryAndAuditFields(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-workflow-denied-resolved"
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	payload := []byte(`{"kind":"workflow_approval_context","version":1,"workflow":{"workflow_id":"wf-b2b-denied","run_id":"run-b2b-denied","step_id":"approve","step_name":"Manager Approval"},"decision":{"amount":8800,"currency":"USD","vendor":"Contoso Travel","escalation_reason":"budget threshold exceeded"}}`)
	require.NoError(t, s.memStore.PutContext(ctx, ctxKey, payload))

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      capsdk.SubjectWorkflowApprovalGate,
		ContextPtr: ctxPtr,
		TenantId:   "default",
		Labels: map[string]string{
			"workflow_id": "wf-b2b-denied",
			"run_id":      "run-b2b-denied",
			"step_id":     "approve",
			"gate_type":   "workflow_approval",
		},
	}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, req))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, req))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateDenied))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-b2b-denied",
		JobHash:          "hash-b2b-denied",
		Reason:           "finance approval required",
	}))
	require.NoError(t, s.jobStore.SetApprovalRecord(ctx, jobID, store.ApprovalRecord{
		ApprovedBy:     "manager-2",
		ApprovedRole:   "manager",
		ApprovedAt:     1709000002000000,
		Reason:         "rejected",
		Note:           "over budget for this quarter",
		PolicySnapshot: "snap-b2b-denied",
		JobHash:        "hash-b2b-denied",
	}))

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, httpReq)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	require.Len(t, items, 1)

	item := items[0].(map[string]any)
	summary, ok := item["decision_summary"].(map[string]any)
	require.True(t, ok, "expected decision_summary map")
	assert.Equal(t, "workflow_payload", summary["source"])
	assert.Equal(t, "available", summary["context_status"])
	assert.Equal(t, "Contoso Travel", summary["vendor"])
	assert.Equal(t, "budget threshold exceeded", summary["why"])
	assert.Equal(t, "snap-b2b-denied", item["policy_snapshot"])
	assert.Equal(t, "hash-b2b-denied", item["job_hash"])
	assert.Equal(t, "manager-2", item["resolved_by"])
	assert.Equal(t, "over budget for this quarter", item["resolved_comment"])
	assert.Equal(t, "rejected", item["resolution"])
}

func TestRejectJobWithEmptySafetyDecision(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-reject-empty-sd"
	// Set up with minimal safety decision — simulates the case where
	// safety decision data is missing/incomplete.
	setupApprovalJob(t, s, jobID, "", model.SafetyDecisionRecord{})

	body := `{"reason":"denied by admin"}`
	req := httptest.NewRequest("POST", "/api/v1/jobs/"+jobID+"/reject", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("job_id", jobID)
	rr := httptest.NewRecorder()
	s.handleRejectJob(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "rejection should succeed with empty safety decision; body: %s", rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, jobID, resp["job_id"])

	// Verify the job was actually rejected.
	state, err := s.jobStore.GetState(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, model.JobStateDenied, state)
}

func TestListApprovalsTenantFiltering(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Create jobs for two different tenants.
	for i, tenant := range []string{"tenant-a", "tenant-b", "tenant-a"} {
		jobID := "job-tenant-" + string(rune('0'+i))
		setupApprovalJob(t, s, jobID, tenant, model.SafetyDecisionRecord{
			Decision:         model.SafetyRequireApproval,
			ApprovalRequired: true,
			PolicySnapshot:   "snap-test",
			JobHash:          "hash" + string(rune('0'+i)),
		})
	}

	// With no auth (s.auth == nil), all tenants pass — should see 3 items.
	req := httptest.NewRequest("GET", "/api/v1/approvals?include_resolved=false", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	assert.Len(t, items, 3, "with no auth, all tenant items should be returned")
}

func TestLockReleaseBoundedContext(t *testing.T) {
	s, _, _ := newTestGateway(t)

	jobID := "job-lock-test"

	// withApprovalLock should acquire, execute, and release the lock cleanly.
	var called bool
	err := s.withApprovalLock(context.Background(), jobID, func(ctx context.Context) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called, "lock closure should have been called")

	// Verify the lock was released by successfully re-acquiring it.
	err = s.withApprovalLock(context.Background(), jobID, func(ctx context.Context) error {
		return nil
	})
	require.NoError(t, err, "should be able to re-acquire lock after release")
}

func TestApproveJobIdempotent(t *testing.T) {
	s, _, sc := newTestGateway(t)
	sc.setSnapshots([]string{"snap-test"})

	jobID := "job-idempotent"
	setupApprovalJobWithHash(t, s, jobID, "")

	// First approval.
	body := `{"reason":"approved"}`
	req := httptest.NewRequest("POST", "/api/v1/jobs/"+jobID+"/approve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("job_id", jobID)
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "first approval: %s", rr.Body.String())

	// Second approval (idempotent) — should return OK, not 409.
	req2 := httptest.NewRequest("POST", "/api/v1/jobs/"+jobID+"/approve", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.SetPathValue("job_id", jobID)
	rr2 := httptest.NewRecorder()
	s.handleApproveJob(rr2, req2)
	require.Equal(t, http.StatusOK, rr2.Code, "idempotent approval: %s", rr2.Body.String())

	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(rr2.Body.Bytes(), &resp2))
	assert.Equal(t, "already_approved", resp2["status"])
}

func TestRejectJobIdempotent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-reject-idempotent"
	setupApprovalJob(t, s, jobID, "", model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-test",
		JobHash:          "hash1",
	})

	// First rejection.
	body := `{"reason":"denied"}`
	req := httptest.NewRequest("POST", "/api/v1/jobs/"+jobID+"/reject", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("job_id", jobID)
	rr := httptest.NewRecorder()
	s.handleRejectJob(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "first rejection: %s", rr.Body.String())

	// Verify state is denied.
	state, err := s.jobStore.GetState(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, model.JobStateDenied, state)

	// Second rejection (idempotent) — should return OK with already_rejected.
	req2 := httptest.NewRequest("POST", "/api/v1/jobs/"+jobID+"/reject", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.SetPathValue("job_id", jobID)
	rr2 := httptest.NewRecorder()
	s.handleRejectJob(rr2, req2)
	require.Equal(t, http.StatusOK, rr2.Code, "idempotent rejection: %s", rr2.Body.String())

	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(rr2.Body.Bytes(), &resp2))
	assert.Equal(t, "already_rejected", resp2["status"])
}

func TestApproveJobNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest("POST", "/api/v1/jobs/nonexistent/approve", nil)
	req.SetPathValue("job_id", "nonexistent")
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestRejectJobNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest("POST", "/api/v1/jobs/nonexistent/reject", nil)
	req.SetPathValue("job_id", "nonexistent")
	rr := httptest.NewRecorder()
	s.handleRejectJob(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestListApprovalsIncludesTimedOutApprovals(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-timeout-approval"
	// Create a job in APPROVAL state with approval_required safety decision.
	setupApprovalJob(t, s, jobID, "", model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-test",
		JobHash:          "hash-" + jobID,
	})
	// Transition to TIMEOUT (simulating reconciler deadline expiration).
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateTimeout))

	req := httptest.NewRequest("GET", "/api/v1/approvals", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	assert.Len(t, items, 1, "timed-out approval job should appear in the list")

	item := items[0].(map[string]any)
	job := item["job"].(map[string]any)
	assert.Equal(t, "TIMEOUT", job["state"])
}

func TestListApprovalsExcludesNonApprovalTimeoutJobs(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-worker-timeout"
	// Create a plain worker job (not approval-gated) and transition it to TIMEOUT
	// through a valid state path: "" -> PENDING -> SCHEDULED -> DISPATCHED -> TIMEOUT.
	pbReq := &pb.JobRequest{JobId: jobID, Topic: "test.worker.topic"}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, pbReq))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, pbReq))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStatePending))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateScheduled))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateDispatched))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateTimeout))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision: model.SafetyAllow,
	}))

	req := httptest.NewRequest("GET", "/api/v1/approvals", nil)
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	assert.Len(t, items, 0, "non-approval timeout job should NOT appear")
}
