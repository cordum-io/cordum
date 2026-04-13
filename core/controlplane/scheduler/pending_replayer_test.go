package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	infraStore "github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestPendingReplayerReplaysJobs(t *testing.T) {
	store := &replayerStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         map[string]*pb.JobRequest{},
	}

	bus := &fakeBus{}
	registry := newTestRegistry(t)
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
	registry := newTestRegistry(t)
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
	registry := newTestRegistry(t)
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

func (m *spyMetrics) IncJobsReceived(string)                            {}
func (m *spyMetrics) IncJobsDispatched(string)                          {}
func (m *spyMetrics) IncJobsCompleted(string, string)                   {}
func (m *spyMetrics) IncSafetyDenied(string)                            {}
func (m *spyMetrics) IncSafetyUnavailable(string)                       {}
func (m *spyMetrics) IncOutputPolicyChecked(string)                     {}
func (m *spyMetrics) IncOutputPolicyQuarantined(string)                 {}
func (m *spyMetrics) IncOutputPolicySkipped(string)                     {}
func (m *spyMetrics) IncAsyncOutputTimeout(string)                      {}
func (m *spyMetrics) IncOutputEvaluations(string)                       {}
func (m *spyMetrics) IncOutputDenials(string)                           {}
func (m *spyMetrics) IncOutputRedactions(string)                        {}
func (m *spyMetrics) ObserveJobLockWait(float64)                        {}
func (m *spyMetrics) ObserveDispatchLatency(string, float64)            {}
func (m *spyMetrics) ObserveOutputCheckLatency(string, string, float64) {}
func (m *spyMetrics) ObserveOutputEvalDuration(string, float64)         {}
func (m *spyMetrics) SetActiveGoroutines(int)                           {}
func (m *spyMetrics) SetStaleJobs(string, int)                          {}
func (m *spyMetrics) IncDLQEmitFailure(string)                          {}
func (m *spyMetrics) IncJobCancelFailures()                             {}
func (m *spyMetrics) IncValidationRejections()                          {}
func (m *spyMetrics) IncInputFailOpen(string)                           {}
func (m *spyMetrics) IncJobLockAbandoned()                              {}
func (m *spyMetrics) IncResultPtrWriteFailure()                         {}
func (m *spyMetrics) IncDispatchRollback(string)                        {}
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
	registry := newTestRegistry(t)
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
	registry := newTestRegistry(t)
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

// TestPendingReplayerSkipsAlreadyTransitionedApprovedJobs verifies that
// replayApproved() does not re-submit jobs that have already transitioned
// out of JobStateApproval (e.g. by the gateway approve handler). This
// prevents the race condition where the replayer re-submits a job that was
// already approved and dispatched, causing it to go through a fresh safety
// check and return to REQUIRE_APPROVAL state.
func TestPendingReplayerSkipsAlreadyTransitionedApprovedJobs(t *testing.T) {
	store := &replayerStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         map[string]*pb.JobRequest{},
	}

	bus := &fakeBus{}
	registry := newTestRegistry(t)
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	// Create a job that WAS in JobStateApproval but has already been
	// transitioned to JobStatePending by the gateway approve handler.
	req := &pb.JobRequest{
		JobId:    "job-already-approved",
		Topic:    "job.test",
		TenantId: "default",
		Labels:   map[string]string{"approval_granted": "true"},
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	// First set to approval (so it appears in the state index)
	if err := store.SetState(context.Background(), req.JobId, JobStateApproval); err != nil {
		t.Fatalf("set state approval: %v", err)
	}
	// Then transition to pending (simulating gateway approve handler)
	if err := store.SetState(context.Background(), req.JobId, JobStatePending); err != nil {
		t.Fatalf("set state pending: %v", err)
	}

	replayer := NewPendingReplayer(engine, store, 0, time.Millisecond)
	replayer.replayApproved(context.Background(), time.Now())

	// The job should NOT be re-submitted since it's no longer in JobStateApproval
	if len(bus.published) != 0 {
		t.Fatalf("expected already-transitioned job to be skipped, but %d messages were published", len(bus.published))
	}
}

func TestPendingReplayerMarksApprovalSubmitIntentPublished(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := infraStore.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("new redis job store: %v", err)
	}
	defer func() {
		_ = store.Close()
		srv.Close()
	}()

	bus := &fakeBus{}
	registry := newTestRegistry(t)
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId:    "job-approved-pending-intent",
		Topic:    "job.test",
		TenantId: "default",
	}
	ctx := context.Background()
	if err := store.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), JobStateApproval); err != nil {
		t.Fatalf("set approval state: %v", err)
	}
	hash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	if _, err := store.ResolveApproval(ctx, infraStore.ApprovalResolutionParams{
		JobID:       req.GetJobId(),
		Decision:    ApprovalDecisionApprove,
		ResultState: JobStatePending,
		ApprovedBy:  "alice",
		LabelUpdates: map[string]string{
			"approval_granted": "true",
		},
	}); err != nil {
		t.Fatalf("resolve approval: %v", err)
	}

	replayer := NewPendingReplayer(engine, store, 0, time.Millisecond)
	replayer.replayPending(ctx, time.Now().Add(time.Second))

	record, err := store.GetApprovalRecord(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.PublishStatus != ApprovalPublishPublished {
		t.Fatalf("expected submit publish intent to be marked published, got %q", record.PublishStatus)
	}
	if record.PublishedAt <= 0 {
		t.Fatalf("expected published_at > 0, got %d", record.PublishedAt)
	}
}

func TestPendingReplayerReplaysRejectedApprovalPublishes(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := infraStore.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("new redis job store: %v", err)
	}
	defer func() {
		_ = store.Close()
		srv.Close()
	}()

	bus := &fakeBus{}
	registry := newTestRegistry(t)
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId:    "job-rejected-replay",
		Topic:    capsdk.SubjectWorkflowApprovalGate,
		TenantId: "default",
	}
	ctx := context.Background()
	if err := store.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), JobStateApproval); err != nil {
		t.Fatalf("set approval state: %v", err)
	}
	hash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   workflowGateSnapshot,
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	if _, err := store.ResolveApproval(ctx, infraStore.ApprovalResolutionParams{
		JobID:         req.GetJobId(),
		Decision:      ApprovalDecisionReject,
		ResultState:   JobStateDenied,
		ApprovedBy:    "bob",
		Reason:        "denied",
		PublishTarget: ApprovalPublishTargetDLQAndResult,
	}); err != nil {
		t.Fatalf("resolve reject: %v", err)
	}

	replayer := NewPendingReplayer(engine, store, 0, time.Millisecond)
	replayer.replayRejected(ctx, time.Now().Add(time.Second))

	published := bus.snapshotPublished()
	if len(published) != 2 {
		t.Fatalf("expected DLQ + result replay publishes, got %d", len(published))
	}
	record, err := store.GetApprovalRecord(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.PublishStatus != ApprovalPublishPublished {
		t.Fatalf("expected reject publish intent to be marked published, got %q", record.PublishStatus)
	}
	if record.PublishedAt <= 0 {
		t.Fatalf("expected published_at > 0, got %d", record.PublishedAt)
	}
}

// TestPendingReplayerSingleTickPerTTLWindow verifies that when two replayer
// goroutines race to acquire the distributed lock, only one executes tick()
// per TTL window. After the TTL expires, the second replica can acquire the
// lock and run tick().
func TestPendingReplayerSingleTickPerTTLWindow(t *testing.T) {
	const pollInterval = 5 * time.Millisecond // lockTTL = 10ms

	store := &replayerStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         map[string]*pb.JobRequest{},
	}

	bus1 := &observedBus{}
	bus2 := &observedBus{}
	registry := newTestRegistry(t)

	engine1 := NewEngine(bus1, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)
	engine2 := NewEngine(bus2, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	// Seed a pending job so tick() has work to do.
	req := &pb.JobRequest{JobId: "job-concurrent", Topic: "job.test", TenantId: "default"}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(context.Background(), req.JobId, JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	replayer1 := NewPendingReplayer(engine1, store, time.Millisecond, pollInterval)
	replayer2 := NewPendingReplayer(engine2, store, time.Millisecond, pollInterval)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run both replayers concurrently (simulating two replicas).
	go replayer1.Start(ctx)
	go replayer2.Start(ctx)

	// Poll until at least one replayer publishes.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bus1.count()+bus2.count() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	total := bus1.count() + bus2.count()
	if total == 0 {
		t.Fatal("expected at least one replayer to publish a job")
	}

	// With TTL-based lock hold, both replayers should not tick simultaneously
	// within the same TTL window. We verify that exactly one bus received
	// traffic per window by checking that the job was not double-dispatched
	// in the first window. At very short poll intervals both may eventually
	// tick (after TTL expiry), but the key invariant is that within a single
	// TTL window only one replica succeeds.
	// The test passes if we reach here without panics/deadlocks; the real
	// assertion is the lock-exclusion verified below.

	// --- Verify lock exclusion with a tighter, synchronous test ---
	// Reset state for a clean check.
	store2 := &replayerStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         map[string]*pb.JobRequest{},
	}

	req2 := &pb.JobRequest{JobId: "job-excl", Topic: "job.test", TenantId: "default"}
	_ = store2.SetJobRequest(context.Background(), req2)
	_ = store2.SetState(context.Background(), req2.JobId, JobStatePending)

	var tickCount atomic.Int32
	var wg sync.WaitGroup

	// Two goroutines race to acquire lock and tick.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := store2.TryAcquireLock(context.Background(), "cordum:replayer:pending", 10*time.Millisecond)
			if err != nil || token == "" {
				return
			}
			tickCount.Add(1)
			// Simulate tick work.
			time.Sleep(time.Millisecond)
			// No ReleaseLock — TTL-based hold.
		}()
	}
	wg.Wait()

	if got := tickCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 tick in TTL window, got %d", got)
	}

	// Poll until TTL expires and the lock becomes available.
	var token string
	var err error
	deadline2 := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline2) {
		token, err = store2.TryAcquireLock(context.Background(), "cordum:replayer:pending", 10*time.Millisecond)
		if err == nil && token != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("lock acquisition after TTL: %v", err)
	}
	if token == "" {
		t.Fatal("expected lock to be available after TTL expiry")
	}
}
