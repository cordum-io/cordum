package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
)

func withAuth(req *http.Request, auth *AuthContext) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), authContextKey{}, auth))
}

func TestApproveJobBindsSnapshotAndHash(t *testing.T) {
	s, bus, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-1"})

	jobID := uuid.NewString()
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
		Labels:   map[string]string{"workflow_id": "wf-1"},
	}
	if err := s.jobStore.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(context.Background(), jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	body := `{"reason":"ok","note":"looks fine"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(body))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	rr := httptest.NewRecorder()

	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	state, err := s.jobStore.GetState(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != model.JobStatePending {
		t.Fatalf("expected pending got %s", state)
	}
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.ApprovedBy != "alice" {
		t.Fatalf("expected approved_by alice got %q", record.ApprovedBy)
	}
	if record.ApprovedRole != "admin" {
		t.Fatalf("expected approved_role admin got %q", record.ApprovedRole)
	}
	if record.PolicySnapshot != "snap-1" {
		t.Fatalf("expected policy snapshot snap-1 got %q", record.PolicySnapshot)
	}
	if record.JobHash != hash {
		t.Fatalf("expected job hash %q got %q", hash, record.JobHash)
	}
	if record.ApprovedAt <= 0 {
		t.Fatalf("expected ApprovedAt > 0, got %d", record.ApprovedAt)
	}
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(bus.published))
	}
	if bus.published[0].subject != capsdk.SubjectSubmit {
		t.Fatalf("expected publish to %s got %s", capsdk.SubjectSubmit, bus.published[0].subject)
	}
}

func TestApproveJobUsesAuthContextForApprover(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-1"})

	setupApprovalJob := func(jobID, principalID string) string {
		req := &pb.JobRequest{
			JobId:       jobID,
			Topic:       "job.test",
			TenantId:    "default",
			PrincipalId: principalID,
		}
		if err := s.jobStore.SetJobMeta(context.Background(), req); err != nil {
			t.Fatalf("set job meta: %v", err)
		}
		if err := s.jobStore.SetJobRequest(context.Background(), req); err != nil {
			t.Fatalf("set job req: %v", err)
		}
		if err := s.jobStore.SetState(context.Background(), jobID, model.JobStateApproval); err != nil {
			t.Fatalf("set state: %v", err)
		}
		hash, err := scheduler.HashJobRequest(req)
		if err != nil {
			t.Fatalf("hash job: %v", err)
		}
		if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, model.SafetyDecisionRecord{
			Decision:         model.SafetyRequireApproval,
			ApprovalRequired: true,
			PolicySnapshot:   "snap-1",
			JobHash:          hash,
		}); err != nil {
			t.Fatalf("set safety decision: %v", err)
		}
		return hash
	}

	jobID := "job-approval-no-auth"
	submitter := "user-submitter"
	_ = setupApprovalJob(jobID, submitter)

	noAuthReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", nil)
	noAuthReq.Header.Set("X-Tenant-ID", "default")
	noAuthReq.SetPathValue("job_id", jobID)
	noAuthReq.Header.Set("X-Principal-Id", "spoofed-user")
	noAuthRec := httptest.NewRecorder()
	s.handleApproveJob(noAuthRec, noAuthReq)
	if noAuthRec.Code != http.StatusOK {
		t.Fatalf("no auth approve: expected 200 got %d body=%s", noAuthRec.Code, noAuthRec.Body.String())
	}
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.ApprovedBy != "system/unknown" {
		t.Fatalf("expected approved_by system/unknown got %q", record.ApprovedBy)
	}
	if record.ApprovedBy == submitter {
		t.Fatalf("approved_by should not match submitter %q", submitter)
	}

	jobID = "job-approval-with-auth"
	_ = setupApprovalJob(jobID, submitter)

	authReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", nil)
	authReq.Header.Set("X-Tenant-ID", "default")
	authReq.SetPathValue("job_id", jobID)
	authReq = withAuth(authReq, &AuthContext{Tenant: "default", PrincipalID: "approver-1", Role: "admin"})
	authRec := httptest.NewRecorder()
	s.handleApproveJob(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("auth approve: expected 200 got %d body=%s", authRec.Code, authRec.Body.String())
	}
	record, err = s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.ApprovedBy != "approver-1" {
		t.Fatalf("expected approved_by approver-1 got %q", record.ApprovedBy)
	}
}

func TestApproveJobRejectsOnSnapshotMismatch(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-2"})

	jobID := "job-mismatch"
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
	}
	if err := s.jobStore.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(context.Background(), jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", nil)
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 got %d body=%s", rr.Code, rr.Body.String())
	}
	state, _ := s.jobStore.GetState(context.Background(), jobID)
	if state != model.JobStateApproval {
		t.Fatalf("expected approval state got %s", state)
	}
}

func TestRejectJobStoresApprovalRecord(t *testing.T) {
	s, bus, _ := newTestGateway(t)

	jobID := "job-reject"
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
	}
	if err := s.jobStore.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(context.Background(), jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          "hash",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	body := `{"reason":"nope","note":"not safe"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/reject", strings.NewReader(body))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &AuthContext{Tenant: "default", PrincipalID: "bob", Role: "admin"})
	rr := httptest.NewRecorder()
	s.handleRejectJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	state, err := s.jobStore.GetState(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != model.JobStateDenied {
		t.Fatalf("expected denied got %s", state)
	}
	if len(bus.published) == 0 {
		t.Fatalf("expected DLQ publish")
	}
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.ApprovedBy != "bob" {
		t.Fatalf("expected approved_by bob got %q", record.ApprovedBy)
	}
	if record.Reason != "nope" {
		t.Fatalf("expected reason nope got %q", record.Reason)
	}
	if record.Note != "not safe" {
		t.Fatalf("expected note not safe got %q", record.Note)
	}
	if record.ApprovedAt <= 0 {
		t.Fatalf("expected ApprovedAt > 0 on rejection, got %d", record.ApprovedAt)
	}
}

func TestListApprovalsIncludesJobHash(t *testing.T) {
	s, _, _ := newTestGateway(t)

	jobID := "job-approval-hash"
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
	}
	if err := s.jobStore.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(context.Background(), jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          "hash-123",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil)
	httpReq.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) == 0 {
		t.Fatalf("expected approvals")
	}
	if payload.Items[0]["job_hash"] != "hash-123" {
		t.Fatalf("expected job_hash, got %#v", payload.Items[0]["job_hash"])
	}
}

func TestGetJobIncludesApprovalMetadata(t *testing.T) {
	s, _, _ := newTestGateway(t)

	jobID := "job-approval-metadata"
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
	}
	if err := s.jobStore.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(context.Background(), jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := s.jobStore.SetApprovalRecord(context.Background(), jobID, store.ApprovalRecord{
		ApprovedBy:     "carol",
		ApprovedRole:   "admin",
		Reason:         "ok",
		Note:           "note",
		PolicySnapshot: "snap-1",
		JobHash:        "hash",
	}); err != nil {
		t.Fatalf("set approval record: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, model.SafetyDecisionRecord{
		Decision:       model.SafetyAllow,
		PolicySnapshot: "snap-1",
		JobHash:        "hash",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("id", jobID)
	rr := httptest.NewRecorder()
	s.handleGetJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["approval_by"] != "carol" {
		t.Fatalf("expected approval_by carol got %#v", payload["approval_by"])
	}
	if payload["safety_job_hash"] != "hash" {
		t.Fatalf("expected safety_job_hash hash got %#v", payload["safety_job_hash"])
	}
	if payload["approval_job_hash"] != "hash" {
		t.Fatalf("expected approval_job_hash hash got %#v", payload["approval_job_hash"])
	}
}

func TestApproveJobDoubleApproveIdempotent(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-1"})

	jobID := uuid.NewString()
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
		Labels:   map[string]string{"workflow_id": "wf-1"},
	}
	if err := s.jobStore.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(context.Background(), jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	approve := func() *httptest.ResponseRecorder {
		body := `{"reason":"ok","note":"looks fine"}`
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(body))
		httpReq.Header.Set("X-Tenant-ID", "default")
		httpReq.SetPathValue("job_id", jobID)
		httpReq = withAuth(httpReq, &AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
		rr := httptest.NewRecorder()
		s.handleApproveJob(rr, httpReq)
		return rr
	}

	// First approval should succeed.
	rr1 := approve()
	if rr1.Code != http.StatusOK {
		t.Fatalf("first approve: expected 200 got %d body=%s", rr1.Code, rr1.Body.String())
	}

	// Second approval should return 409 — job is no longer awaiting approval.
	rr2 := approve()
	if rr2.Code != http.StatusConflict {
		t.Fatalf("second approve: expected 409 got %d body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestApproveJobConcurrentRace(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-1"})

	jobID := uuid.NewString()
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
		Labels:   map[string]string{"workflow_id": "wf-1"},
	}
	if err := s.jobStore.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(context.Background(), jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	const N = 10
	var wg sync.WaitGroup
	var okCount, conflictCount atomic.Int32

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			body := `{"reason":"ok","note":"concurrent"}`
			httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(body))
			httpReq.Header.Set("X-Tenant-ID", "default")
			httpReq.SetPathValue("job_id", jobID)
			httpReq = withAuth(httpReq, &AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
			rr := httptest.NewRecorder()
			s.handleApproveJob(rr, httpReq)

			switch rr.Code {
			case http.StatusOK:
				okCount.Add(1)
			case http.StatusConflict:
				conflictCount.Add(1)
			default:
				t.Errorf("unexpected status %d body=%s", rr.Code, rr.Body.String())
			}
		}()
	}
	wg.Wait()

	if got := okCount.Load(); got < 1 {
		t.Errorf("expected at least 1 approval success, got %d", got)
	}
	if total := okCount.Load() + conflictCount.Load(); total != N {
		t.Errorf("expected %d total responses (200+409), got %d", N, total)
	}
}
