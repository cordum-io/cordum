package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/google/uuid"
)

func withAuth(req *http.Request, authCtx *auth.AuthContext) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
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
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
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

func TestApproveJobMarksPublishIntentComplete(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-1"})

	jobID := uuid.NewString()
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

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(`{"reason":"ok"}`))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.PublishStatus != model.ApprovalPublishPublished {
		t.Fatalf("expected published approval submit intent, got %q", record.PublishStatus)
	}
	if record.PublishTarget != model.ApprovalPublishTargetSubmit {
		t.Fatalf("expected submit publish target, got %q", record.PublishTarget)
	}
	if record.PublishedAt <= 0 {
		t.Fatalf("expected published_at > 0, got %d", record.PublishedAt)
	}
}

func TestApproveJobPublishFailureLeavesPendingIntent(t *testing.T) {
	s, bus, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-1"})
	bus.publishErr = errors.New("submit unavailable")
	bus.failSubject = capsdk.SubjectSubmit

	jobID := uuid.NewString()
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

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(`{"reason":"ok"}`))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 got %d body=%s", rr.Code, rr.Body.String())
	}
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.PublishStatus != model.ApprovalPublishPending {
		t.Fatalf("expected pending publish intent, got %q", record.PublishStatus)
	}
	if record.PublishTarget != model.ApprovalPublishTargetSubmit {
		t.Fatalf("expected submit publish target, got %q", record.PublishTarget)
	}
	if record.PublishedAt != 0 {
		t.Fatalf("expected published_at to remain 0, got %d", record.PublishedAt)
	}
}

func TestApproveWorkflowGateBypassesSafetySnapshotCheck(t *testing.T) {
	s, bus, safety := newTestGateway(t)
	// Intentionally mismatch snapshots; workflow gates should bypass this check.
	safety.setSnapshots([]string{"different-snapshot"})

	jobID := uuid.NewString()
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    capsdk.SubjectApprovalGate,
		TenantId: "default",
		Labels: map[string]string{
			"workflow_id": "wf-1",
			"run_id":      "run-1",
			"step_id":     "approval",
			"gate_type":   "workflow_approval",
		},
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
		JobHash:          hash,
		// Deliberately empty — workflow gates do not bind to safety snapshots.
		PolicySnapshot: "",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(`{"reason":"ok"}`))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	rr := httptest.NewRecorder()

	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.PolicySnapshot != "workflow-gate" {
		t.Fatalf("expected synthetic workflow snapshot, got %q", record.PolicySnapshot)
	}
	if len(bus.published) != 1 || bus.published[0].subject != capsdk.SubjectSubmit {
		t.Fatalf("expected approval publish to %s, got %#v", capsdk.SubjectSubmit, bus.published)
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
	authReq = withAuth(authReq, &auth.AuthContext{Tenant: "default", PrincipalID: "approver-1", Role: "admin"})
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

func TestApproveJobRefreshesStaleSnapshot(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-2"})

	jobID := "job-mismatch"
	req := &pb.JobRequest{
		JobId:       jobID,
		Topic:       "job.test",
		TenantId:    "default",
		PrincipalId: "agent-submitter",
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

	// Stale snapshot should be refreshed — approval proceeds (200 OK).
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	state, _ := s.jobStore.GetState(context.Background(), jobID)
	if state == model.JobStateApproval {
		t.Fatalf("expected state to move past approval, got %s", state)
	}
}

func TestApproveJobRejectsWhenSnapshotUnavailable(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots(nil) // safety kernel returns no snapshots

	jobID := "job-no-snap"
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

	// Safety kernel unreachable — should return 502.
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 got %d body=%s", rr.Code, rr.Body.String())
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

	body := `{"reason":"nope","note":"not safe"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/reject", strings.NewReader(body))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "bob", Role: "admin"})
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

func TestRejectJobMarksPublishIntentComplete(t *testing.T) {
	s, _, _ := newTestGateway(t)

	jobID := "job-reject-publish-complete"
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

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/reject", strings.NewReader(`{"reason":"nope"}`))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "bob", Role: "admin"})
	rr := httptest.NewRecorder()
	s.handleRejectJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.PublishStatus != model.ApprovalPublishPublished {
		t.Fatalf("expected published reject intent, got %q", record.PublishStatus)
	}
	if record.PublishTarget != model.ApprovalPublishTargetDLQ {
		t.Fatalf("expected dlq publish target, got %q", record.PublishTarget)
	}
	if record.PublishedAt <= 0 {
		t.Fatalf("expected published_at > 0, got %d", record.PublishedAt)
	}
}

func TestRejectWorkflowGateResultPublishFailureLeavesPendingIntent(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	bus.publishErr = errors.New("result unavailable")
	bus.failSubject = capsdk.SubjectResult

	jobID := "job-reject-workflow-gate-pending"
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    capsdk.SubjectWorkflowApprovalGate,
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
		PolicySnapshot:   "workflow-gate",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/reject", strings.NewReader(`{"reason":"denied"}`))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "bob", Role: "admin"})
	rr := httptest.NewRecorder()
	s.handleRejectJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.PublishStatus != model.ApprovalPublishPending {
		t.Fatalf("expected pending reject publish intent, got %q", record.PublishStatus)
	}
	if record.PublishTarget != model.ApprovalPublishTargetDLQAndResult {
		t.Fatalf("expected dlq_and_result publish target, got %q", record.PublishTarget)
	}
	if record.PublishedAt != 0 {
		t.Fatalf("expected published_at to remain 0, got %d", record.PublishedAt)
	}
}

func TestRepairApprovalDryRunClassifiesTerminalWorkflowRun(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-1"})

	ctx := context.Background()
	now := time.Now().UTC()
	run := &wf.WorkflowRun{
		ID:         "run-repair-terminal",
		WorkflowID: "wf-repair-terminal",
		OrgID:      "default",
		Status:     wf.RunStatusSucceeded,
		Steps:      map[string]*wf.StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	jobID := "job-repair-terminal"
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
		Labels: map[string]string{
			"run_id":      run.ID,
			"workflow_id": run.WorkflowID,
		},
	}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/repair", strings.NewReader(`{}`))
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	rr := httptest.NewRecorder()
	s.handleRepairApproval(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	plan, _ := payload["plan"].(map[string]any)
	if got, _ := plan["kind"].(string); got != "invalidate_terminal_run" {
		t.Fatalf("expected terminal run repair plan, got %q", got)
	}
	if got, _ := plan["repairable"].(bool); !got {
		t.Fatalf("expected repairable plan, got %#v", plan)
	}
	if applied, _ := payload["applied"].(bool); applied {
		t.Fatalf("expected dry-run response, got %#v", payload)
	}
}

func TestRepairApprovalDryRunClassifiesStaleSnapshot(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-current"})

	ctx := context.Background()
	jobID := "job-repair-stale-snapshot"
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
	}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-legacy",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/repair", strings.NewReader(`{}`))
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	rr := httptest.NewRecorder()
	s.handleRepairApproval(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	plan, _ := payload["plan"].(map[string]any)
	if got, _ := plan["kind"].(string); got != "invalidate_stale_snapshot" {
		t.Fatalf("expected stale snapshot repair plan, got %q", got)
	}
}

func TestRepairApprovalApplyApprovedResolutionPublishesAndAudits(t *testing.T) {
	s, bus, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-1"})
	auditSender := &testAuditSender{}
	s.auditExporter = auditSender

	ctx := context.Background()
	jobID := "job-repair-approved"
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
		Labels: map[string]string{
			"approval_granted": "true",
		},
	}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	body := `{"apply":true,"note":"operator fix"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/repair", strings.NewReader(body))
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	rr := httptest.NewRecorder()
	s.handleRepairApproval(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	state, err := s.jobStore.GetState(ctx, jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != model.JobStatePending {
		t.Fatalf("expected pending state, got %s", state)
	}
	record, err := s.jobStore.GetApprovalRecord(ctx, jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.Status != model.ApprovalStatusApproved {
		t.Fatalf("expected approved status, got %q", record.Status)
	}
	if record.PublishStatus != model.ApprovalPublishPublished {
		t.Fatalf("expected published repair intent, got %q", record.PublishStatus)
	}
	if !strings.Contains(record.Note, "repair: operator fix") {
		t.Fatalf("expected repair note to be persisted, got %q", record.Note)
	}
	if len(bus.published) != 1 || bus.published[0].subject != capsdk.SubjectSubmit {
		t.Fatalf("expected repaired approval to publish submit once, got %#v", bus.published)
	}
	if auditSender.Len() == 0 {
		t.Fatal("expected repair audit event")
	}
	if got := auditSender.Get(0).Action; got != "repair" {
		t.Fatalf("expected repair audit action, got %q", got)
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

func TestListApprovalsIncludesResolutionFields(t *testing.T) {
	s, _, _ := newTestGateway(t)

	jobID := "job-approval-resolution"
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
		JobHash:          "hash-res",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	approvalAt := int64(1709000000000000) // fixed microsecond timestamp
	if err := s.jobStore.SetApprovalRecord(context.Background(), jobID, store.ApprovalRecord{
		ApprovedBy:     "alice",
		ApprovedRole:   "admin",
		ApprovedAt:     approvalAt,
		Reason:         "ok",
		Note:           "looks fine",
		PolicySnapshot: "snap-1",
		JobHash:        "hash-res",
	}); err != nil {
		t.Fatalf("set approval record: %v", err)
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
	item := payload.Items[0]
	if item["resolved_by"] != "alice" {
		t.Fatalf("expected resolved_by alice got %#v", item["resolved_by"])
	}
	if item["resolved_comment"] != "looks fine" {
		t.Fatalf("expected resolved_comment 'looks fine' got %#v", item["resolved_comment"])
	}
	resolvedAt, ok := item["resolved_at"].(float64)
	if !ok || int64(resolvedAt) != approvalAt {
		t.Fatalf("expected resolved_at %d got %#v", approvalAt, item["resolved_at"])
	}
}

func TestListResolvedWorkflowApprovalsRetainsDecisionSummaryAndAuditFields(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	jobID := "job-approval-workflow-resolved"
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	if err := s.memStore.PutContext(ctx, ctxKey, []byte(`{"kind":"workflow_approval_context","version":1,"workflow":{"workflow_id":"wf-b2b","run_id":"run-b2b","step_id":"approve","step_name":"Manager Approval"},"decision":{"amount":2500,"currency":"USD","vendor":"Acme Travel","escalation_reason":"budget threshold exceeded"}}`)); err != nil {
		t.Fatalf("put context: %v", err)
	}

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      capsdk.SubjectWorkflowApprovalGate,
		ContextPtr: ctxPtr,
		TenantId:   "default",
		Labels: map[string]string{
			"workflow_id": "wf-b2b",
			"run_id":      "run-b2b",
			"step_id":     "approve",
			"gate_type":   "workflow_approval",
		},
	}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set approval state: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateSucceeded); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := s.jobStore.SetTenant(ctx, jobID, "default"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-b2b",
		JobHash:          "hash-b2b",
		Reason:           "finance approval required",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	if err := s.jobStore.SetApprovalRecord(ctx, jobID, store.ApprovalRecord{
		ApprovedBy:     "manager-1",
		ApprovedRole:   "manager",
		ApprovedAt:     1709000001000000,
		Reason:         "approved",
		Note:           "within quarterly budget",
		PolicySnapshot: "snap-b2b",
		JobHash:        "hash-b2b",
	}); err != nil {
		t.Fatalf("set approval record: %v", err)
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

	item := payload.Items[0]
	summary, ok := item["decision_summary"].(map[string]any)
	if !ok {
		t.Fatalf("expected decision_summary object got %#v", item["decision_summary"])
	}
	if summary["source"] != "workflow_payload" {
		t.Fatalf("expected workflow_payload source got %#v", summary["source"])
	}
	if summary["context_status"] != "available" {
		t.Fatalf("expected available context status got %#v", summary["context_status"])
	}
	if summary["vendor"] != "Acme Travel" {
		t.Fatalf("expected vendor Acme Travel got %#v", summary["vendor"])
	}
	if item["policy_snapshot"] != "snap-b2b" || item["job_hash"] != "hash-b2b" {
		t.Fatalf("expected audit metadata preserved got snapshot=%#v hash=%#v", item["policy_snapshot"], item["job_hash"])
	}
	if item["resolved_by"] != "manager-1" {
		t.Fatalf("expected resolved_by manager-1 got %#v", item["resolved_by"])
	}
}

func TestListApprovalsOmitsResolutionFieldsWhenNoRecord(t *testing.T) {
	s, _, _ := newTestGateway(t)

	jobID := "job-approval-no-resolution"
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
		JobHash:          "hash-none",
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
	item := payload.Items[0]
	if _, exists := item["resolved_by"]; exists {
		t.Fatalf("expected no resolved_by field, got %#v", item["resolved_by"])
	}
	if _, exists := item["resolved_comment"]; exists {
		t.Fatalf("expected no resolved_comment field, got %#v", item["resolved_comment"])
	}
	if _, exists := item["resolved_at"]; exists {
		t.Fatalf("expected no resolved_at field, got %#v", item["resolved_at"])
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
		httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
		rr := httptest.NewRecorder()
		s.handleApproveJob(rr, httpReq)
		return rr
	}

	// First approval should succeed.
	rr1 := approve()
	if rr1.Code != http.StatusOK {
		t.Fatalf("first approve: expected 200 got %d body=%s", rr1.Code, rr1.Body.String())
	}

	// Second approval should return 200 with "already_approved" (idempotent).
	rr2 := approve()
	if rr2.Code != http.StatusOK {
		t.Fatalf("second approve: expected 200 (idempotent) got %d body=%s", rr2.Code, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), "already_approved") {
		t.Fatalf("second approve: expected already_approved in body, got %s", rr2.Body.String())
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
			httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
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

func TestApproveJob_RejectsTimedOutRun(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-1"})

	ctx := context.Background()

	// Create a timed-out workflow run.
	now := time.Now().UTC()
	wfDef := &wf.Workflow{
		ID:         "wf-timeout-test",
		OrgID:      "default",
		TimeoutSec: 10,
		Steps:      map[string]*wf.Step{"s1": {ID: "s1", Type: wf.StepTypeWorker, Topic: "job.test"}},
	}
	if err := s.workflowStore.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &wf.WorkflowRun{
		ID:          "run-timedout-1",
		WorkflowID:  wfDef.ID,
		OrgID:       "default",
		Status:      wf.RunStatusTimedOut,
		Steps:       map[string]*wf.StepRun{},
		CreatedAt:   now,
		UpdatedAt:   now,
		CompletedAt: &now,
	}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Create a job in APPROVAL_REQUIRED state that references the timed-out run.
	jobID := uuid.NewString()
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
		Labels: map[string]string{
			"workflow_id": wfDef.ID,
			"run_id":      run.ID,
			"step_id":     "s1",
		},
	}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	// Attempt to approve — should get 409 with a clear timeout message.
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(`{}`))
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "default", PrincipalID: "alice", Role: "admin"})
	rr := httptest.NewRecorder()

	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d body=%s", rr.Code, rr.Body.String())
	}
	var errResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	errMsg, _ := errResp["error"].(string)
	if !strings.Contains(errMsg, "timed_out") {
		t.Fatalf("expected error to mention 'timed_out', got %q", errMsg)
	}
}

func TestListApprovalsExcludesTerminatedRunApprovals(t *testing.T) {
	s, _, _ := newTestGateway(t)

	ctx := context.Background()
	runID := uuid.NewString()

	// Create a workflow run in terminal (succeeded) state.
	run := &wf.WorkflowRun{
		ID:         runID,
		WorkflowID: "wf-stale",
		Status:     wf.RunStatusSucceeded,
		Steps:      map[string]*wf.StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Create a stale approval job (belongs to the terminated run).
	staleJobID := uuid.NewString()
	staleReq := &pb.JobRequest{
		JobId:    staleJobID,
		Topic:    "job.mkt-authority.approval",
		TenantId: "default",
		Labels:   map[string]string{"run_id": runID, "workflow_id": "wf-stale"},
	}
	if err := s.jobStore.SetJobMeta(ctx, staleReq); err != nil {
		t.Fatalf("set stale job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, staleReq); err != nil {
		t.Fatalf("set stale job req: %v", err)
	}
	if err := s.jobStore.SetState(ctx, staleJobID, model.JobStateApproval); err != nil {
		t.Fatalf("set stale state: %v", err)
	}

	// Create a standalone approval job (no run_id — should appear in list).
	freshJobID := uuid.NewString()
	freshReq := &pb.JobRequest{
		JobId:    freshJobID,
		Topic:    "job.test.approval",
		TenantId: "default",
		Labels:   map[string]string{},
	}
	if err := s.jobStore.SetJobMeta(ctx, freshReq); err != nil {
		t.Fatalf("set fresh job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, freshReq); err != nil {
		t.Fatalf("set fresh job req: %v", err)
	}
	if err := s.jobStore.SetState(ctx, freshJobID, model.JobStateApproval); err != nil {
		t.Fatalf("set fresh state: %v", err)
	}

	// List approvals.
	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals?include_resolved=false", nil)
	httpReq.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()

	s.handleListApprovals(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The stale approval should be filtered out; only the fresh one remains.
	for _, item := range resp.Items {
		job, ok := item["job"].(map[string]any)
		if !ok {
			continue
		}
		if job["id"] == staleJobID {
			t.Fatalf("stale approval (terminated run) should have been filtered out")
		}
	}

	found := false
	for _, item := range resp.Items {
		job, ok := item["job"].(map[string]any)
		if !ok {
			continue
		}
		if job["id"] == freshJobID {
			found = true
		}
	}
	if !found {
		t.Fatalf("standalone approval should be in the list")
	}
}
