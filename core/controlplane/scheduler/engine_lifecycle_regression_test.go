package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	infraStore "github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestProcessJobPublishFailureScheduledReplayIncrementsAttempts(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	jobStore, err := infraStore.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("new redis job store: %v", err)
	}
	t.Cleanup(func() { _ = jobStore.Close() })

	bus := &fakeBus{publishErr: errors.New("bus unavailable")}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-publish-retry",
		Topic: "job.default",
	}

	for i := 0; i < 2; i++ {
		err := engine.handleJobRequest(req, "trace-publish-retry")
		if err == nil {
			t.Fatalf("attempt %d: expected retryable error", i+1)
		}
		if _, ok := err.(*retryableError); !ok {
			t.Fatalf("attempt %d: expected retryableError, got %T (%v)", i+1, err, err)
		}
	}

	attempts, err := jobStore.GetAttempts(context.Background(), req.GetJobId())
	if err != nil {
		t.Fatalf("get attempts: %v", err)
	}
	// Each iteration: SCHEDULED (+1) → DISPATCHED → publish fail → rollback to SCHEDULED (+1) = 2 per iteration.
	// Two iterations = 4 total scheduling attempts.
	if attempts != 4 {
		t.Fatalf("expected 4 scheduling attempts (2 per publish-failed replay with rollback), got %d", attempts)
	}
}

type flakyStateReadStore struct {
	*fakeJobStore
	jobID   string
	readErr error
	calls   int32
}

func (s *flakyStateReadStore) GetState(ctx context.Context, jobID string) (JobState, error) {
	if jobID == s.jobID && atomic.AddInt32(&s.calls, 1) == 1 {
		return "", s.readErr
	}
	return s.fakeJobStore.GetState(ctx, jobID)
}

func TestHandleJobRequestStateReadErrorDoesNotDispatchDuplicate(t *testing.T) {
	store := &flakyStateReadStore{
		fakeJobStore: newFakeJobStore(),
		jobID:        "job-running",
		readErr:      errors.New("redis timeout"),
	}
	store.states["job-running"] = JobStateRunning
	store.topics["job-running"] = "job.default"

	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{JobId: "job-running", Topic: "job.default"}
	err := engine.handleJobRequest(req, "trace-state-read")
	if err == nil {
		t.Fatal("expected retryable error on non-nil state-read failure")
	}
	if _, ok := err.(*retryableError); !ok {
		t.Fatalf("expected retryableError, got %T (%v)", err, err)
	}
	if len(bus.published) != 0 {
		t.Fatalf("expected no dispatch when state read fails, got %d publishes", len(bus.published))
	}
	if got := store.states["job-running"]; got != JobStateRunning {
		t.Fatalf("expected job to remain RUNNING, got %s", got)
	}

	// Second call sees RUNNING state and should be a no-op without dispatch.
	if err := engine.handleJobRequest(req, "trace-state-read-2"); err != nil {
		t.Fatalf("second call should be no-op, got %v", err)
	}
	if len(bus.published) != 0 {
		t.Fatalf("expected no dispatch after recovered state read, got %d publishes", len(bus.published))
	}
}
