package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/memory"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

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
	if err := s.jobStore.SetState(context.Background(), jobID, scheduler.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, scheduler.SafetyDecisionRecord{
		Decision:         scheduler.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	body := `{"reason":"ok","note":"looks fine"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(body))
	httpReq.SetPathValue("job_id", jobID)
	httpReq.Header.Set("X-Principal-Id", "alice")
	httpReq.Header.Set("X-Principal-Role", "secops")
	rr := httptest.NewRecorder()

	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	state, err := s.jobStore.GetState(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != scheduler.JobStatePending {
		t.Fatalf("expected pending got %s", state)
	}
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.ApprovedBy != "alice" {
		t.Fatalf("expected approved_by alice got %q", record.ApprovedBy)
	}
	if record.ApprovedRole != "secops" {
		t.Fatalf("expected approved_role secops got %q", record.ApprovedRole)
	}
	if record.PolicySnapshot != "snap-1" {
		t.Fatalf("expected policy snapshot snap-1 got %q", record.PolicySnapshot)
	}
	if record.JobHash != hash {
		t.Fatalf("expected job hash %q got %q", hash, record.JobHash)
	}
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(bus.published))
	}
	if bus.published[0].subject != capsdk.SubjectSubmit {
		t.Fatalf("expected publish to %s got %s", capsdk.SubjectSubmit, bus.published[0].subject)
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
	if err := s.jobStore.SetState(context.Background(), jobID, scheduler.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, scheduler.SafetyDecisionRecord{
		Decision:         scheduler.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", nil)
	httpReq.SetPathValue("job_id", jobID)
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 got %d body=%s", rr.Code, rr.Body.String())
	}
	state, _ := s.jobStore.GetState(context.Background(), jobID)
	if state != scheduler.JobStateApproval {
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
	if err := s.jobStore.SetState(context.Background(), jobID, scheduler.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, scheduler.SafetyDecisionRecord{
		Decision:         scheduler.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          "hash",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	body := `{"reason":"nope","note":"not safe"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/reject", strings.NewReader(body))
	httpReq.SetPathValue("job_id", jobID)
	httpReq.Header.Set("X-Principal-Id", "bob")
	httpReq.Header.Set("X-Principal-Role", "secops")
	rr := httptest.NewRecorder()
	s.handleRejectJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	state, err := s.jobStore.GetState(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != scheduler.JobStateDenied {
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
	if err := s.jobStore.SetState(context.Background(), jobID, scheduler.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, scheduler.SafetyDecisionRecord{
		Decision:         scheduler.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          "hash-123",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil)
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
	if err := s.jobStore.SetState(context.Background(), jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := s.jobStore.SetApprovalRecord(context.Background(), jobID, memory.ApprovalRecord{
		ApprovedBy:     "carol",
		ApprovedRole:   "secops",
		Reason:         "ok",
		Note:           "note",
		PolicySnapshot: "snap-1",
		JobHash:        "hash",
	}); err != nil {
		t.Fatalf("set approval record: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, scheduler.SafetyDecisionRecord{
		Decision:       scheduler.SafetyAllow,
		PolicySnapshot: "snap-1",
		JobHash:        "hash",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
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
