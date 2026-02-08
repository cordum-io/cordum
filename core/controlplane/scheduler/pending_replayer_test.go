package scheduler

import (
	"context"
	"sync"
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

type observedBus struct {
	mu        sync.Mutex
	published int
}

func (b *observedBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	b.published++
	b.mu.Unlock()
	return nil
}

func (b *observedBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	return nil
}

func (b *observedBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.published
}

func TestPendingReplayerStartTriggersTick(t *testing.T) {
	store := &replayerStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         map[string]*pb.JobRequest{},
	}
	bus := &observedBus{}
	registry := NewMemoryRegistry()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{JobId: "job-start", Topic: "job.test", TenantId: "default"}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(context.Background(), req.JobId, JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	replayer := NewPendingReplayer(engine, store, time.Millisecond, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go replayer.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bus.count() > 0 {
			cancel()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if bus.count() == 0 {
		t.Fatalf("expected pending replayer to publish job")
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

func TestPendingReplayerReplaysApprovedJobs(t *testing.T) {
	store := &replayerStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         map[string]*pb.JobRequest{},
	}

	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	// Create a job in APPROVAL_REQUIRED state with approval_granted label
	req := &pb.JobRequest{
		JobId:    "job-approved",
		Topic:    "job.test",
		TenantId: "default",
		Labels:   map[string]string{"approval_granted": "true"},
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(context.Background(), req.JobId, JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}

	replayer := NewPendingReplayer(engine, store, 0, time.Millisecond)
	replayer.replayApproved(context.Background(), time.Now())

	if len(bus.published) == 0 {
		t.Fatalf("expected approved job to be republished")
	}
	state, err := store.GetState(context.Background(), req.JobId)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	// Job should now be running after replay
	if state != JobStateRunning {
		t.Fatalf("expected job running, got %s", state)
	}
}

// spyMetrics tracks IncOrphanReplayed calls.
type spyMetrics struct {
	mu             sync.Mutex
	orphanReplayed map[string]int
}

func newSpyMetrics() *spyMetrics {
	return &spyMetrics{orphanReplayed: map[string]int{}}
}

func (m *spyMetrics) IncJobsReceived(string)                {}
func (m *spyMetrics) IncJobsDispatched(string)              {}
func (m *spyMetrics) IncJobsCompleted(string, string)       {}
func (m *spyMetrics) IncSafetyDenied(string)                {}
func (m *spyMetrics) IncSafetyUnavailable(string)           {}
func (m *spyMetrics) ObserveJobLockWait(float64)            {}
func (m *spyMetrics) ObserveDispatchLatency(string, float64) {}
func (m *spyMetrics) SetActiveGoroutines(int)               {}
func (m *spyMetrics) SetStaleJobs(string, int)              {}
func (m *spyMetrics) IncOrphanReplayed(topic string) {
	m.mu.Lock()
	m.orphanReplayed[topic]++
	m.mu.Unlock()
}

func (m *spyMetrics) getOrphanCount(topic string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.orphanReplayed[topic]
}

func TestPendingReplayerOrphanMetric(t *testing.T) {
	store := &replayerStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         map[string]*pb.JobRequest{},
	}

	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{JobId: "orphan-1", Topic: "job.replay-test", TenantId: "default"}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(context.Background(), req.JobId, JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	metrics := newSpyMetrics()
	replayer := NewPendingReplayer(engine, store, 0, time.Millisecond).WithMetrics(metrics)
	replayer.replayPending(context.Background(), time.Now())

	if len(bus.published) == 0 {
		t.Fatalf("expected orphaned job to be republished")
	}
	if got := metrics.getOrphanCount("job.replay-test"); got != 1 {
		t.Fatalf("expected IncOrphanReplayed called once for topic, got %d", got)
	}
}

func TestPendingReplayerSkipsUnapprovedJobs(t *testing.T) {
	store := &replayerStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         map[string]*pb.JobRequest{},
	}

	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	// Create a job in APPROVAL_REQUIRED state WITHOUT approval_granted label
	req := &pb.JobRequest{
		JobId:    "job-unapproved",
		Topic:    "job.test",
		TenantId: "default",
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(context.Background(), req.JobId, JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}

	replayer := NewPendingReplayer(engine, store, 0, time.Millisecond)
	replayer.replayApproved(context.Background(), time.Now())

	if len(bus.published) != 0 {
		t.Fatalf("expected unapproved job to be skipped, but was republished")
	}
	state, err := store.GetState(context.Background(), req.JobId)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	// Job should still be in approval state
	if state != JobStateApproval {
		t.Fatalf("expected job still in approval state, got %s", state)
	}
}
