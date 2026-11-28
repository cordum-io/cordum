package store

// Evidence for task-8bab7c41's fix-forward: the scheduler reserves a job with
// no stored tenant under model.DefaultTenant, and every terminal write frees
// that slot, so the reservation and the cleanup use one key.

import (
	"context"
	"fmt"
	"testing"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestReserveTenantSlotTenantlessJobFreedByTerminalWrites(t *testing.T) {
	tests := []struct {
		name string
		// approval parks the job in APPROVAL before the terminal write, set up as
		// the reject test does; stale also changes the stored request after the
		// approved hash was taken, as the stale-request repair test does.
		approval, stale bool
		terminal        func(ctx context.Context, s *RedisJobStore, jobID string) error
	}{
		{"set_state", false, false, func(ctx context.Context, s *RedisJobStore, jobID string) error {
			return s.SetState(ctx, jobID, model.JobStateSucceeded)
		}},
		{"cancel", false, false, func(ctx context.Context, s *RedisJobStore, jobID string) error {
			_, err := s.CancelJob(ctx, jobID)
			return err
		}},
		{"project_job_result", false, false, func(ctx context.Context, s *RedisJobStore, jobID string) error {
			return s.ProjectJobResult(ctx, jobID, model.JobStateSucceeded, "", "")
		}},
		{"resolve_approval", true, false, func(ctx context.Context, s *RedisJobStore, jobID string) error {
			_, err := s.ResolveApproval(ctx, ApprovalResolutionParams{
				JobID: jobID, Decision: model.ApprovalDecisionReject, ResultState: model.JobStateDenied,
				ApprovedBy: "bob", Reason: "denied",
			})
			return err
		}},
		{"approval_repair", true, true, func(ctx context.Context, s *RedisJobStore, jobID string) error {
			snapshot, err := s.InspectApprovalRepair(ctx, jobID)
			if err != nil {
				return err
			}
			plan := ClassifyApprovalRepair(*snapshot, ApprovalRepairClassifyOptions{})
			if plan.Kind != ApprovalRepairInvalidateStaleRequest {
				return fmt.Errorf("repair plan %q, want %q", plan.Kind, ApprovalRepairInvalidateStaleRequest)
			}
			_, err = s.ApplyApprovalRepair(ctx, ApprovalRepairApplyParams{JobID: jobID, Plan: plan, Actor: "operator-2", Note: "stale request"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newDispatchFencingTestStore(t)
			ctx := context.Background()
			jobID := "job-" + tt.name
			req := &pb.JobRequest{JobId: jobID, Topic: "job.test"} // no tenant anywhere
			hash, err := hashApprovalJobRequest(req)
			if err != nil {
				t.Fatalf("hash request: %v", err)
			}
			if tt.stale {
				req.Labels = map[string]string{"priority": "high"}
			}
			if err := s.SetJobMeta(ctx, req); err != nil {
				t.Fatalf("SetJobMeta: %v", err)
			}
			if err := s.SetJobRequest(ctx, req); err != nil {
				t.Fatalf("SetJobRequest: %v", err)
			}
			if err := s.SetState(ctx, jobID, model.JobStatePending); err != nil {
				t.Fatalf("SetState(PENDING): %v", err)
			}
			if active, err := s.CountActiveByTenant(ctx, model.DefaultTenant); err != nil || active != 0 {
				t.Fatalf("PENDING with no stored tenant joined the %s set: active = %d, %v; want 0", model.DefaultTenant, active, err)
			}
			if ok, _, err := s.ReserveTenantSlot(ctx, model.DefaultTenant, jobID, 1); err != nil || !ok {
				t.Fatalf("ReserveTenantSlot(%s) at cap 1 = %v, %v; want admitted", jobID, ok, err)
			}
			if tt.approval {
				if err := s.SetState(ctx, jobID, model.JobStateApproval); err != nil {
					t.Fatalf("SetState(APPROVAL): %v", err)
				}
				if err := s.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
					Decision: model.SafetyRequireApproval, ApprovalRequired: true, PolicySnapshot: "snap-1", JobHash: hash,
				}); err != nil {
					t.Fatalf("SetSafetyDecision: %v", err)
				}
			}
			if err := tt.terminal(ctx, s, jobID); err != nil {
				t.Fatalf("terminal write: %v", err)
			}
			if active, err := s.CountActiveByTenant(ctx, model.DefaultTenant); err != nil || active != 0 {
				t.Errorf("after the terminal write the job still holds its %s slot: active = %d, %v; want 0", model.DefaultTenant, active, err)
			}
			if ok, _, err := s.ReserveTenantSlot(ctx, model.DefaultTenant, "next-"+jobID, 1); err != nil || !ok {
				t.Errorf("next job's ReserveTenantSlot at cap 1 = %v, %v; want admitted", ok, err)
			}
		})
	}
}
