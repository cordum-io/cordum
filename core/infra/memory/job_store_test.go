package memory

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
)

func TestRedisJobStoreStateAndResultPtr(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	jobID := "job-123"

	if err := store.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	state, err := store.GetState(ctx, jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != scheduler.JobStatePending {
		t.Fatalf("expected state %s, got %s", scheduler.JobStatePending, state)
	}

	resultPtr := "redis://res:job-123"
	if err := store.SetResultPtr(ctx, jobID, resultPtr); err != nil {
		t.Fatalf("set result ptr: %v", err)
	}

	gotPtr, err := store.GetResultPtr(ctx, jobID)
	if err != nil {
		t.Fatalf("get result ptr: %v", err)
	}
	if gotPtr != resultPtr {
		t.Fatalf("expected result ptr %s, got %s", resultPtr, gotPtr)
	}

	// advance state and list by state
	if err := store.SetState(ctx, jobID, scheduler.JobStateScheduled); err != nil {
		t.Fatalf("advance state: %v", err)
	}
	records, err := store.ListJobsByState(ctx, scheduler.JobStateScheduled, time.Now().Unix(), 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(records) != 1 || records[0].ID != jobID {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestRedisJobStoreTransitionGuard(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	jobID := "job-456"

	if err := store.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	// invalid backwards transition
	if err := store.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("same state should be ok: %v", err)
	}
	if err := store.SetState(ctx, jobID, scheduler.JobStateDispatched); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobID, scheduler.JobStateScheduled); err == nil {
		t.Fatalf("expected invalid backward transition")
	}
}

func TestRedisJobStoreListRecentJobs(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create three jobs with different timestamps
	if err := store.SetState(ctx, "job-a", scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.SetState(ctx, "job-b", scheduler.JobStateDispatched); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.SetState(ctx, "job-c", scheduler.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}

	list, err := store.ListRecentJobs(ctx, 2)
	if err != nil {
		t.Fatalf("ListRecentJobs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(list))
	}
	if list[0].ID != "job-c" || list[1].ID != "job-b" {
		t.Fatalf("unexpected order: %#v", list)
	}
	if list[0].State != scheduler.JobStateRunning {
		t.Fatalf("expected state RUNNING, got %s", list[0].State)
	}
}

func TestRedisJobStoreTopicMetadata(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	jobID := "job-topic"
	if err := store.SetTopic(ctx, jobID, "job.chat.simple"); err != nil {
		t.Fatalf("set topic: %v", err)
	}
	got, err := store.GetTopic(ctx, jobID)
	if err != nil {
		t.Fatalf("get topic: %v", err)
	}
	if got != "job.chat.simple" {
		t.Fatalf("expected topic job.chat.simple, got %s", got)
	}

	if err := store.SetTenant(ctx, jobID, "tenant-a"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	tenant, err := store.GetTenant(ctx, jobID)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tenant != "tenant-a" {
		t.Fatalf("expected tenant tenant-a, got %s", tenant)
	}

	if err := store.SetSafetyDecision(ctx, jobID, "DENY", "forbidden"); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	dec, reason, err := store.GetSafetyDecision(ctx, jobID)
	if err != nil {
		t.Fatalf("get safety decision: %v", err)
	}
	if dec != "DENY" || reason != "forbidden" {
		t.Fatalf("unexpected safety decision %s reason %s", dec, reason)
	}
}
