package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestApproveJobAuditFailureLeavesApprovalUnchanged(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	jobID := seedApprovalJob(t, s, "")
	ctx := context.Background()
	before, err := s.jobStore.GetApprovalRecord(ctx, jobID)
	require.NoError(t, err)
	requestBefore, err := s.jobStore.GetJobRequest(ctx, jobID)
	require.NoError(t, err)
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, approvalDecisionRequest(t, jobID, "approve", `{}`, "", "alice"))

	require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "approval not applied: its audit entry was not stored")
	after, err := s.jobStore.GetApprovalRecord(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	state, err := s.jobStore.GetState(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStateApproval, state)
	requestAfter, err := s.jobStore.GetJobRequest(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, requestBefore, requestAfter)
	require.Empty(t, s.bus.(*stubBus).published)
	require.Empty(t, sink.events)
	require.Len(t, auditPersistErrors(logs, "approve"), 1)
	token, err := s.jobStore.TryAcquireLock(ctx, approvalLockPrefix+jobID, approvalLockTTL)
	require.NoError(t, err)
	require.NotEmpty(t, token, "failed approval left the lock held")
	require.NoError(t, s.jobStore.ReleaseLock(ctx, approvalLockPrefix+jobID, token))
}

func TestRepairApprovalAuditFailureReportsAppliedRepair(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	jobID := seedApprovalJob(t, s, "")
	ctx := context.Background()
	request, err := s.jobStore.GetJobRequest(ctx, jobID)
	require.NoError(t, err)
	request.Labels = map[string]string{"approval_granted": "true", "approval_snapshot": "snap-test"}
	require.NoError(t, s.jobStore.SetJobRequest(ctx, request))
	require.NoError(t, s.jobStore.SetApprovalRecord(ctx, jobID, model.ApprovalRecord{ApprovedBy: "bob"}))
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)

	rr := httptest.NewRecorder()
	s.handleRepairApproval(rr, approvalDecisionRequest(t, jobID, "repair", `{"apply":true}`, "", "alice"))

	require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "approval repair applied but its audit entry was not stored")
	state, err := s.jobStore.GetState(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatePending, state)
	record, err := s.jobStore.GetApprovalRecord(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, model.ApprovalStatusApproved, record.Status)
	require.Equal(t, model.ApprovalPublishPublished, record.PublishStatus)
	published := s.bus.(*stubBus).published
	require.Len(t, published, 1)
	require.Equal(t, capsdk.SubjectSubmit, published[0].subject)
	require.Len(t, auditPersistErrors(logs, "repair"), 1)
}

func TestApproveJobAuditPersistsAndExportsOriginalFields(t *testing.T) {
	s, _, _, _ := auditPersistServer(t)
	jobID := seedApprovalJob(t, s, "")
	ctx := context.Background()
	agent, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		Name: "approval agent", TenantID: "default", Owner: "team", RiskTier: "high",
	})
	require.NoError(t, err)
	request, err := s.jobStore.GetJobRequest(ctx, jobID)
	require.NoError(t, err)
	request.Labels = map[string]string{"agent_id": agent.ID}
	require.NoError(t, s.jobStore.SetJobRequest(ctx, request))
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	r := approvalDecisionRequest(t, jobID, "approve", `{"reason":"reviewed","note":"ready"}`, "", "alice")
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, r)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	entries, err := s.loadPolicyAudit(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entry := entries[0]
	actor, source, label := policybundles.PolicyActorIdentity(r)
	require.Equal(t, policybundles.PolicyAuditEntry{
		ID: entry.ID, CreatedAt: entry.CreatedAt, Action: "approve", ResourceType: "job",
		ResourceID: jobID, ResourceName: request.Topic, ActorID: actor, Role: "admin",
		IdentitySource: source, IdentityLabel: label, Reason: "reviewed", Message: "approve job " + jobID,
		AgentID: agent.ID, AgentName: agent.Name, AgentRiskTier: agent.RiskTier,
		Extra: map[string]string{"note": "ready"},
	}, entry)
	require.Len(t, sink.events, 1)
	require.Equal(t, policybundles.AuditEntryToSIEM(entry, s.tenant), sink.events[0])
	api := httptest.NewRecorder()
	s.handleListPolicyAudit(api, r)
	require.Equal(t, http.StatusOK, api.Code, api.Body.String())
	var response struct {
		Items []policybundles.PolicyAuditEntry `json:"items"`
	}
	require.NoError(t, json.Unmarshal(api.Body.Bytes(), &response))
	require.Equal(t, entries, response.Items)
}

func TestApproveJobResolutionFailureRecordsFailedAttempt(t *testing.T) {
	for _, failure := range []string{"conflict", "persist"} {
		t.Run(failure, func(t *testing.T) {
			s, _, hooked, _ := auditPersistServer(t)
			jobID := seedApprovalJob(t, s, "")
			status := http.StatusInternalServerError
			if failure == "conflict" {
				status = http.StatusConflict
				hooked.AddHook(auditPersistHook{getKey: auditPersistLogKey, fired: &atomic.Bool{}, afterGet: func() {
					require.NoError(t, s.jobStore.SetState(context.Background(), jobID, model.JobStateDenied))
				}})
			} else {
				s.jobStore.Client().(*redis.Client).AddHook(auditPersistHook{failKey: "job:meta:" + jobID})
			}
			rr := httptest.NewRecorder()
			s.handleApproveJob(rr, approvalDecisionRequest(t, jobID, "approve", `{}`, "", "alice"))
			require.Equal(t, status, rr.Code, rr.Body.String())
			require.Equal(t, []string{"approve_failed", "approve"}, auditPersistActions(t, s))
			require.Empty(t, s.bus.(*stubBus).published)
			record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
			require.NoError(t, err)
			require.Empty(t, record.ApprovedBy)
		})
	}
}

func TestRejectJobAuditFailureStillRejects(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	jobID := seedApprovalJob(t, s, "")
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)
	rr := httptest.NewRecorder()
	s.handleRejectJob(rr, approvalDecisionRequest(t, jobID, "reject", `{}`, "", "alice"))

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	state, err := s.jobStore.GetState(context.Background(), jobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStateDenied, state)
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	require.NoError(t, err)
	require.Equal(t, model.ApprovalDecisionReject, record.Decision)
	require.Len(t, auditPersistErrors(logs, "reject"), 1)
}

func TestCancelRunAuditFailureStillCancels(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	s.workflowEng = wf.NewEngine(s.workflowStore, s.bus)
	run := &wf.WorkflowRun{ID: "audit-cancel", OrgID: "default", Status: wf.RunStatusRunning}
	require.NoError(t, s.workflowStore.CreateRun(context.Background(), run))
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)
	r := approvalDecisionRequest(t, "", "cancel", `{}`, "", "alice")
	r.SetPathValue("run_id", run.ID)
	rr := httptest.NewRecorder()
	s.handleCancelRun(rr, r)

	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	after, err := s.workflowStore.GetRun(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, wf.RunStatusCancelled, after.Status)
	require.Len(t, auditPersistErrors(logs, "cancel"), 1)
}

func TestSelfApprovalAuditFailureStillRefuses(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	jobID := seedApprovalJob(t, s, "")
	r := approvalDecisionRequest(t, jobID, "approve", `{}`, "", "alice")
	identity := submitterIdentity(r)
	require.NotEmpty(t, identity)
	require.NoError(t, s.jobStore.SetSubmittedBy(context.Background(), jobID, identity))
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, r)

	require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
	state, err := s.jobStore.GetState(context.Background(), jobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStateApproval, state)
	record, err := s.jobStore.GetApprovalRecord(context.Background(), jobID)
	require.NoError(t, err)
	require.Empty(t, record.ApprovedBy)
	require.Empty(t, s.bus.(*stubBus).published)
	require.Len(t, auditPersistErrors(logs, "self_approval_denied"), 1)
}
