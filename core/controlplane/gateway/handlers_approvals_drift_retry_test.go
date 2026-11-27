package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

// An approve that fails after the policy-drift refresh leaves the stored safety
// record as it was, so the same approve succeeds once the store recovers.
func TestApproveAfterDriftRefreshFailureCanBeRetried(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		cmd    func(jobID, runID string) []string
		status int
		want   string
	}{
		// The workflow-run re-check, the second wf:run read.
		{name: "run_recheck", cmd: workflowRunCmd, status: http.StatusServiceUnavailable, want: "workflow run lookup unavailable"},
		// ResolveApproval's own request read, the second job:req read.
		{name: "resolve_request_read", cmd: storedRequestCmd, status: http.StatusInternalServerError, want: "failed to persist approval resolution"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s, b, safety := newTestGateway(t)
			safety.setSnapshots([]string{"snap-2"})
			safety.setResponse(&pb.PolicyCheckResponse{
				Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "allowed", RuleId: "rule-2", PolicySnapshot: "snap-2",
			})
			fault := &approvalReadFault{skip: 1}
			client := s.jobStore.Client()
			client.AddHook(fault)
			s.workflowStore = wf.NewRedisWorkflowStoreFromClient(client)
			jobID, runID, _ := seedStoreReadJob(t, s, storeReadJob{runStatus: wf.RunStatusRunning})
			req, err := s.jobStore.GetJobRequest(ctx, jobID)
			mustNoErr(t, "get job req", err)
			req.PrincipalId = "original-requester" // the drift re-evaluation needs the requester
			mustNoErr(t, "set job req", s.jobStore.SetJobRequest(ctx, req))

			fault.arm(tc.cmd(jobID, runID))
			rr := serveStoreReadDecision(t, s, "approve", jobID, false)
			if fired := fault.disarm(); fired != 1 {
				t.Errorf("fault fired %d times, want 1", fired)
			}
			if rr.Code != tc.status || decodeBody(t, rr)["error"] != tc.want {
				t.Fatalf("first approve = %d %s, want %d %q", rr.Code, rr.Body.String(), tc.status, tc.want)
			}
			assertNoDecisionEffects(t, s, b, jobID)
			rec, err := s.jobStore.GetSafetyDecision(ctx, jobID)
			if err != nil || !rec.ApprovalRequired || rec.PolicySnapshot != "snap-test" {
				t.Fatalf("stored safety = required %v snapshot %q (err %v), want the pre-drift record", rec.ApprovalRequired, rec.PolicySnapshot, err)
			}

			rr = serveStoreReadDecision(t, s, "approve", jobID, false)
			assertDecisionApplied(t, s, b, "approve", jobID, rr)
			approval, err := s.jobStore.GetApprovalRecord(ctx, jobID)
			if err != nil || approval.Decision != model.ApprovalDecisionApprove {
				t.Errorf("approval record decision = %q (err %v), want approve", approval.Decision, err)
			}
			rec, err = s.jobStore.GetSafetyDecision(ctx, jobID)
			if err != nil || rec.ApprovalRequired || rec.Decision != model.SafetyAllow || rec.PolicySnapshot != "snap-2" {
				t.Errorf("stored safety = %+v (err %v), want the refreshed allow under snap-2", rec, err)
			}
			events, err := s.jobStore.GetJobEvents(ctx, jobID)
			mustNoErr(t, "get job events", err)
			resolutions := 0
			for _, event := range events {
				if event.State == string(model.JobStatePending) {
					resolutions++
				}
			}
			if resolutions != 1 {
				t.Errorf("job moved to PENDING %d times, want exactly 1: %+v", resolutions, events)
			}
		})
	}
}

// Approving a job whose stored safety record never required approval still
// answers 409 not actionable and changes nothing.
func TestApproveWithoutRequiredApprovalIsNotActionable(t *testing.T) {
	t.Parallel()
	s, b, _ := newTestGateway(t)
	jobID, _, hash := seedStoreReadJob(t, s, storeReadJob{})
	mustNoErr(t, "set allow safety decision", s.jobStore.SetSafetyDecision(context.Background(), jobID, model.SafetyDecisionRecord{
		Decision: model.SafetyAllow, PolicySnapshot: "snap-test", JobHash: hash,
	}))
	rr := serveStoreReadDecision(t, s, "approve", jobID, false)
	assertErrorCode(t, rr, http.StatusConflict, string(model.ApprovalConflictNotActionable))
	assertNoDecisionEffects(t, s, b, jobID)
}
