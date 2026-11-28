package scheduler

import (
	"context"
	"errors"
	"testing"

	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type tenantReadErrorTestStore struct {
	*infraStore.RedisJobStore
	err   error
	calls int
}

func (s *tenantReadErrorTestStore) GetTenant(_ context.Context, _ string) (string, error) {
	s.calls++
	return "", s.err
}

func TestProcessJobRequeuesWhenStoredTenantReadFails(t *testing.T) {
	js := newTenantSlotTestStore(t)
	readErr := errors.New("tenant read failed")
	store := &tenantReadErrorTestStore{RedisJobStore: js, err: readErr}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	req := &pb.JobRequest{JobId: "job-tenant-read-error", Topic: "job.default", TenantId: "tenant-a"}
	ctx := context.Background()
	if err := js.SetState(ctx, req.GetJobId(), JobStatePending); err != nil {
		t.Fatalf("SetState(PENDING): %v", err)
	}
	if err := js.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("SetJobMeta: %v", err)
	}

	err := engine.processJob(testCtx(t), req, "trace-tenant-read-error")
	var retryErr *retryableError
	if !errors.As(err, &retryErr) || !errors.Is(err, readErr) {
		t.Fatalf("processJob = %v, want retryable error wrapping tenant read failure", err)
	}
	if retryErr.RetryDelay() != retryDelayStore {
		t.Errorf("retry delay = %v, want %v", retryErr.RetryDelay(), retryDelayStore)
	}
	if store.calls == 0 {
		t.Error("GetTenant was never called")
	}
	if got := publishedJobIDs(bus); len(got) != 0 {
		t.Errorf("published %v, want nothing published", got)
	}
	for _, tenant := range []string{"tenant-a", model.DefaultTenant} {
		if n, err := js.CountActiveByTenant(ctx, tenant); err != nil || n != 0 {
			t.Errorf("CountActiveByTenant(%s) = %d, %v; want 0", tenant, n, err)
		}
	}
	if state, err := js.GetState(ctx, req.GetJobId()); err != nil || state != JobStatePending {
		t.Errorf("job state = %s, %v; want PENDING", state, err)
	}
}
