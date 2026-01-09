package scheduler

import (
	"context"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestPendingReplayerReplaysJobs(t *testing.T) {
	store := &replayerStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         map[string]*pb.JobRequest{},
	}

	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{JobId: "job-1", Topic: "job.test", TenantId: "default"}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(context.Background(), req.JobId, JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	replayer := NewPendingReplayer(engine, store, 0, time.Millisecond)
	replayer.replayPending(context.Background(), time.Now())

	if len(bus.published) == 0 {
		t.Fatalf("expected job to be republished")
	}
	state, err := store.GetState(context.Background(), req.JobId)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != JobStateRunning {
		t.Fatalf("expected job running, got %s", state)
	}
}

type replayerStore struct {
	*fakeJobStore
	reqs map[string]*pb.JobRequest
}

func (s *replayerStore) SetJobRequest(_ context.Context, req *pb.JobRequest) error {
	if req == nil || req.JobId == "" {
		return nil
	}
	s.reqs[req.JobId] = req
	return nil
}

func (s *replayerStore) GetJobRequest(_ context.Context, jobID string) (*pb.JobRequest, error) {
	return s.reqs[jobID], nil
}
