package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/cordum/cordum/core/model"
)

func TestRedisJobStoreExposesAtomicResultApply(t *testing.T) {
	if _, ok := reflect.TypeOf((*RedisJobStore)(nil)).MethodByName("ApplyJobResult"); !ok {
		t.Fatal("RedisJobStore does not expose atomic ApplyJobResult")
	}
}

func TestRollbackDispatchCannotClearNewerAttempt(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	if err := store.SetState(ctx, "job-rollback", model.JobStatePending); err != nil {
		t.Fatalf("SetState() error = %v", err)
	}
	staleID, staleAttempt, err := store.BeginDispatch(ctx, "job-rollback", "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch(stale) error = %v", err)
	}
	currentID, currentAttempt, err := store.BeginDispatch(ctx, "job-rollback", "worker-2", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch(current) error = %v", err)
	}
	rollback, ok := any(store).(interface {
		RollbackDispatch(context.Context, string, string, int) (bool, error)
	})
	if !ok {
		t.Fatal("RedisJobStore does not implement CAS RollbackDispatch")
	}
	rolledBack, err := rollback.RollbackDispatch(ctx, "job-rollback", staleID, staleAttempt)
	if err != nil || rolledBack {
		t.Fatalf("RollbackDispatch(stale) = (%v, %v), want (false, nil)", rolledBack, err)
	}
	accepted, err := store.AcceptJobEvent(
		ctx, "job-rollback", currentID, currentAttempt, "worker-2", "tenant-a", "message-1",
	)
	if err != nil || !accepted {
		t.Fatalf("current fence after stale rollback = (%v, %v), want (true, nil)", accepted, err)
	}
}

func TestRollbackDispatchPreservesMonotonicAttemptCounter(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	if err := store.SetState(ctx, "job-rollback-counter", model.JobStatePending); err != nil {
		t.Fatalf("SetState() error = %v", err)
	}
	dispatchID, attempt, err := store.BeginDispatch(
		ctx, "job-rollback-counter", "worker-1", "tenant-a",
	)
	if err != nil {
		t.Fatalf("BeginDispatch(first) error = %v", err)
	}
	rolledBack, err := store.RollbackDispatch(ctx, "job-rollback-counter", dispatchID, attempt)
	if err != nil || !rolledBack {
		t.Fatalf("RollbackDispatch() = (%v, %v), want true", rolledBack, err)
	}
	_, nextAttempt, err := store.BeginDispatch(
		ctx, "job-rollback-counter", "worker-1", "tenant-a",
	)
	if err != nil {
		t.Fatalf("BeginDispatch(second) error = %v", err)
	}
	if nextAttempt != attempt+1 {
		t.Fatalf("attempt after rollback = %d, want %d", nextAttempt, attempt+1)
	}
}
