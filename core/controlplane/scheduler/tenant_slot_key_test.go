package scheduler

// Evidence for task-8bab7c41's fix-forward: processJob reserves a tenant slot
// under the tenant the job meta stores, the key every terminal state write
// frees, so a job with no tenant, or with only Meta.TenantId, gets its slot
// back when it finishes.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestHandleJobRequestReservedSlotFreedAtTerminalState(t *testing.T) {
	tests := []struct {
		name   string
		tenant string // the active set the job counts in
		req    func(jobID string) *pb.JobRequest
	}{
		{"no_tenant", model.DefaultTenant, func(id string) *pb.JobRequest {
			return &pb.JobRequest{JobId: id, Topic: "job.default"}
		}},
		{"meta_tenant_only", "tenant-m", func(id string) *pb.JobRequest {
			return &pb.JobRequest{JobId: id, Topic: "job.default", Meta: &pb.JobMetadata{TenantId: "tenant-m"}}
		}},
		{"tenant_id", "tenant-a", func(id string) *pb.JobRequest {
			return &pb.JobRequest{JobId: id, Topic: "job.default", TenantId: "tenant-a"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			js := newTenantSlotTestStore(t)
			bus := &fakeBus{}
			// The community tier admits 3 concurrent jobs per tenant; no policy cap.
			engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), js, nil)
			ctx := context.Background()
			for i := range 3 {
				id := fmt.Sprintf("job-%d", i)
				if err := engine.handleJobRequest(tt.req(id), "trace-"+id); err != nil {
					t.Fatalf("handleJobRequest(%s): %v", id, err)
				}
				for _, state := range []JobState{JobStateRunning, JobStateSucceeded} {
					if err := js.SetState(ctx, id, state); err != nil {
						t.Fatalf("SetState(%s, %s): %v", id, state, err)
					}
				}
			}
			if got := publishedJobIDs(bus); len(got) != 3 {
				t.Fatalf("published %v, want the first 3 jobs", got)
			}
			for _, tenant := range []string{tt.tenant, model.DefaultTenant} {
				if active, err := js.CountActiveByTenant(ctx, tenant); err != nil || active != 0 {
					t.Errorf("CountActiveByTenant(%s) after 3 terminal jobs = %d, %v; want 0", tenant, active, err)
				}
			}
			if err := engine.handleJobRequest(tt.req("job-3"), "trace-job-3"); err != nil {
				t.Fatalf("handleJobRequest(job-3): %v", err)
			}
			if got := publishedJobIDs(bus); len(got) != 4 {
				t.Fatalf("published %v, want the 4th job dispatched once the first 3 finished", got)
			}
		})
	}
}

// A failed publish rolls the job back, and the deferred release must free the
// slot under the tenant it was reserved under: the stored Meta tenant, not the
// request's ExtractTenant default.
func TestProcessJobReservedSlotReleasedUnderStoredTenant(t *testing.T) {
	js := newTenantSlotTestStore(t)
	engine := NewEngine(&fakeBus{publishErr: errors.New("nats unavailable")}, capOneSafety,
		newTestRegistry(t), NewNaiveStrategy(), js, nil)
	// Written as submitTenantJob writes a job, but with only Meta.TenantId.
	req := &pb.JobRequest{JobId: "job-m", Topic: "job.default", Meta: &pb.JobMetadata{TenantId: "tenant-m"}}
	if err := js.SetState(context.Background(), req.GetJobId(), JobStatePending); err != nil {
		t.Fatalf("SetState(PENDING): %v", err)
	}
	if err := js.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("SetJobMeta: %v", err)
	}
	var retryErr *retryableError
	if err := engine.processJob(testCtx(t), req, "trace-job-m"); !errors.As(err, &retryErr) {
		t.Fatalf("processJob = %v, want a retryable requeue after the failed publish", err)
	}
	for _, tenant := range []string{"tenant-m", model.DefaultTenant} {
		if active, err := js.CountActiveByTenant(context.Background(), tenant); err != nil || active != 0 {
			t.Errorf("the rolled-back job still holds a %s slot: active = %d, %v; want 0", tenant, active, err)
		}
	}
}
