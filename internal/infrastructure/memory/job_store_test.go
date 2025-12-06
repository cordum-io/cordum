package memory

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/yaront1111/cortex-os/core/internal/scheduler"
)

func TestRedisJobStoreStateAndResultPtr(t *testing.T) {
	srv := miniredis.RunT(t)
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
}
