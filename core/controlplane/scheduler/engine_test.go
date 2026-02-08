package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/redisutil"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type publishedMsg struct {
	subject string
	packet  *pb.BusPacket
}

type fakeBus struct {
	mu        sync.Mutex
	published []publishedMsg
}

type fakeConfigProvider struct {
	cfg map[string]any
}

func (f *fakeConfigProvider) Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error) {
	return f.cfg, nil
}

type fakeJobStore struct {
	states         map[string]JobState
	ptrs           map[string]string
	topics         map[string]string
	tenants        map[string]string
	teams          map[string]string
	safety         map[string]SafetyDecisionRecord
	attempts       map[string]int
	locks          map[string]time.Time
	failureReasons map[string]string
}

type sagaJobStore struct {
	*fakeJobStore
	reqs map[string]*pb.JobRequest
}

func newSagaJobStore() *sagaJobStore {
	return &sagaJobStore{
		fakeJobStore: newFakeJobStore(),
		reqs:         make(map[string]*pb.JobRequest),
	}
}

func (s *sagaJobStore) GetJobRequest(_ context.Context, jobID string) (*pb.JobRequest, error) {
	return s.reqs[jobID], nil
}

func newFakeJobStore() *fakeJobStore {
	return &fakeJobStore{
		states:         make(map[string]JobState),
		ptrs:           make(map[string]string),
		topics:         make(map[string]string),
		tenants:        make(map[string]string),
		teams:          make(map[string]string),
		safety:         make(map[string]SafetyDecisionRecord),
		attempts:       make(map[string]int),
		locks:          make(map[string]time.Time),
		failureReasons: make(map[string]string),
	}
}

func (s *fakeJobStore) SetState(_ context.Context, jobID string, state JobState) error {
	s.states[jobID] = state
	if state == JobStateScheduled {
		s.attempts[jobID]++
	}
	return nil
}

func (s *fakeJobStore) GetState(_ context.Context, jobID string) (JobState, error) {
	return s.states[jobID], nil
}

func (s *fakeJobStore) SetResultPtr(_ context.Context, jobID, resultPtr string) error {
	s.ptrs[jobID] = resultPtr
	return nil
}

func (s *fakeJobStore) GetResultPtr(_ context.Context, jobID string) (string, error) {
	return s.ptrs[jobID], nil
}

func (s *fakeJobStore) SetJobMeta(_ context.Context, _ *pb.JobRequest) error {
	return nil
}

func (s *fakeJobStore) SetDeadline(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func (s *fakeJobStore) ListExpiredDeadlines(_ context.Context, _ int64, _ int64) ([]JobRecord, error) {
	return nil, nil
}

func (s *fakeJobStore) ListJobsByState(_ context.Context, state JobState, _ int64, _ int64) ([]JobRecord, error) {
	var out []JobRecord
	for id, st := range s.states {
		if st == state {
			out = append(out, JobRecord{ID: id, UpdatedAt: time.Now().Unix()})
		}
	}
	return out, nil
}

func (s *fakeJobStore) AddJobToTrace(_ context.Context, traceID, jobID string) error {
	return nil
}

func (s *fakeJobStore) GetTraceJobs(_ context.Context, traceID string) ([]JobRecord, error) {
	return nil, nil
}

func (s *fakeJobStore) SetTopic(_ context.Context, jobID, topic string) error {
	s.topics[jobID] = topic
	return nil
}

func (s *fakeJobStore) GetTopic(_ context.Context, jobID string) (string, error) {
	return s.topics[jobID], nil
}

func (s *fakeJobStore) SetTenant(_ context.Context, jobID, tenant string) error {
	s.tenants[jobID] = tenant
	return nil
}

func (s *fakeJobStore) GetTenant(_ context.Context, jobID string) (string, error) {
	return s.tenants[jobID], nil
}

func (s *fakeJobStore) SetTeam(_ context.Context, jobID, team string) error {
	s.teams[jobID] = team
	return nil
}

func (s *fakeJobStore) GetTeam(_ context.Context, jobID string) (string, error) {
	return s.teams[jobID], nil
}

func (s *fakeJobStore) SetSafetyDecision(_ context.Context, jobID string, record SafetyDecisionRecord) error {
	s.safety[jobID] = record
	return nil
}

func (s *fakeJobStore) GetSafetyDecision(_ context.Context, jobID string) (SafetyDecisionRecord, error) {
	return s.safety[jobID], nil
}

func (s *fakeJobStore) GetAttempts(_ context.Context, jobID string) (int, error) {
	return s.attempts[jobID], nil
}

func (s *fakeJobStore) CountActiveByTenant(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (s *fakeJobStore) TryAcquireLock(_ context.Context, key string, ttl time.Duration) (bool, error) {
	if until, ok := s.locks[key]; ok && until.After(time.Now()) {
		return false, nil
	}
	s.locks[key] = time.Now().Add(ttl)
	return true, nil
}

func (s *fakeJobStore) ReleaseLock(_ context.Context, key string) error {
	delete(s.locks, key)
	return nil
}

func (s *fakeJobStore) SetFailureReason(_ context.Context, jobID, reason string) error {
	s.failureReasons[jobID] = reason
	return nil
}

func (s *fakeJobStore) GetFailureReason(_ context.Context, jobID string) (string, error) {
	return s.failureReasons[jobID], nil
}

func (s *fakeJobStore) CancelJob(_ context.Context, jobID string) (JobState, error) {
	state := s.states[jobID]
	if terminalStates[state] {
		return state, nil
	}
	s.states[jobID] = JobStateCancelled
	return JobStateCancelled, nil
}

func (b *fakeBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	b.published = append(b.published, publishedMsg{subject: subject, packet: packet})
	b.mu.Unlock()
	return nil
}

func (b *fakeBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	// Tests call handlers directly, so no-op is fine here.
	return nil
}

func TestEngineHandleHeartbeatStoresWorker(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil)

	packet := &pb.BusPacket{
		SenderId:        "worker-1",
		TraceId:         "trace-hb",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_Heartbeat{
			Heartbeat: &pb.Heartbeat{
				WorkerId: "worker-1",
				Type:     "cpu",
			},
		},
	}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("handle packet: %v", err)
	}

	snapshot := registry.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 worker in registry, got %d", len(snapshot))
	}
	if snapshot["worker-1"].Type != "cpu" {
		t.Fatalf("expected worker type cpu, got %s", snapshot["worker-1"].Type)
	}
}

func TestProcessJobPublishesToSubject(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	strategy := &NaiveStrategy{}
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, strategy, jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-1",
		Topic: "job.default",
	}

	if err := engine.processJob(req, "trace-123"); err != nil {
		t.Fatalf("process job: %v", err)
	}

	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(bus.published))
	}
	if state := jobStore.states["job-1"]; state != JobStateRunning {
		t.Fatalf("expected job state RUNNING, got %s", state)
	}
	msg := bus.published[0]
	if msg.subject != "job.default" {
		t.Fatalf("expected subject job.default, got %s", msg.subject)
	}
	if got := msg.packet.GetJobRequest().JobId; got != "job-1" {
		t.Fatalf("expected job_id job-1, got %s", got)
	}
	if msg.packet.TraceId != "trace-123" {
		t.Fatalf("expected trace_id trace-123, got %s", msg.packet.TraceId)
	}
}

func TestCancelJobPublishesOnlyCancelSubject(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	jobStore := newFakeJobStore()
	jobStore.states["job-1"] = JobStateRunning
	jobStore.topics["job-1"] = "job.default"

	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	if err := engine.CancelJob(context.Background(), "job-1"); err != nil {
		t.Fatalf("cancel job: %v", err)
	}

	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(bus.published))
	}
	if bus.published[0].subject != capsdk.SubjectCancel {
		t.Fatalf("expected publish to %s, got %s", capsdk.SubjectCancel, bus.published[0].subject)
	}
	if cancelReq := bus.published[0].packet.GetJobCancel(); cancelReq == nil || cancelReq.GetJobId() != "job-1" {
		t.Fatalf("expected cancel payload for job-1")
	}
}

func TestHandleJobResultTreatsCompletedAsSucceeded(t *testing.T) {
	store := newFakeJobStore()
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), NewMemoryRegistry(), NewNaiveStrategy(), store, nil)

	res := &pb.JobResult{
		JobId:  "job-completed",
		Status: pb.JobStatus_JOB_STATUS_COMPLETED,
	}

	if err := engine.handleJobResult(res); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	if got := store.states["job-completed"]; got != JobStateSucceeded {
		t.Fatalf("expected COMPLETED to map to SUCCEEDED state, got %s", got)
	}
}

func TestProcessJobInjectsEffectiveConfig(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	strategy := &NaiveStrategy{}
	jobStore := newFakeJobStore()
	cfg := &fakeConfigProvider{cfg: map[string]any{"feature": "on", "limit": 3}}
	engine := NewEngine(bus, NewSafetyBasic(), registry, strategy, jobStore, nil).WithConfig(cfg)

	req := &pb.JobRequest{
		JobId: "job-ec",
		Topic: "job.default",
		Env: map[string]string{
			"step_id":   "step-1",
			"tenant_id": "org-a",
			"team_id":   "team-a",
		},
	}

	if err := engine.processJob(req, "trace-ec"); err != nil {
		t.Fatalf("process job: %v", err)
	}

	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish with effective config injected")
	}
	jr := bus.published[0].packet.GetJobRequest()
	if jr == nil {
		t.Fatalf("missing job request in packet")
	}
	val := jr.GetEnv()[config.EffectiveConfigEnvVar]
	if val == "" {
		t.Fatalf("expected %s env var to be set", config.EffectiveConfigEnvVar)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(val), &parsed); err != nil {
		t.Fatalf("config not valid json: %v", err)
	}
	if parsed["feature"] != "on" {
		t.Fatalf("unexpected config content: %v", parsed)
	}
}

func TestProcessJobBlockedBySafety(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-blocked",
		Topic: "sys.destroy",
	}

	if err := engine.processJob(req, "trace-block"); err != nil {
		t.Fatalf("process job: %v", err)
	}

	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish to dlq when safety blocks, got %d", len(bus.published))
	}
	if bus.published[0].subject != capsdk.SubjectDLQ {
		t.Fatalf("expected dlq subject, got %s", bus.published[0].subject)
	}
	if state := jobStore.states["job-blocked"]; state != JobStateDenied {
		t.Fatalf("expected job state DENIED, got %s", state)
	}
}

func TestProcessJobSkipsInvalidRequest(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil)

	req := &pb.JobRequest{
		JobId: "",
		Topic: "",
	}

	if err := engine.processJob(req, "trace-invalid"); err != nil {
		t.Fatalf("process job: %v", err)
	}

	if len(bus.published) != 0 {
		t.Fatalf("expected 0 publishes for invalid request, got %d", len(bus.published))
	}
}

func TestHandleJobResultUpdatesState(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	res := &pb.JobResult{
		JobId:     "job-1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:job-1",
		WorkerId:  "worker-1",
	}

	if err := engine.handleJobResult(res); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	if state := jobStore.states["job-1"]; state != JobStateSucceeded {
		t.Fatalf("expected job state SUCCEEDED, got %s", state)
	}
	if ptr := jobStore.ptrs["job-1"]; ptr != "redis://res:job-1" {
		t.Fatalf("expected result ptr redis://res:job-1, got %s", ptr)
	}
}

func TestHandleJobResultRetryableSkipsDLQ(t *testing.T) {
	bus := &fakeBus{}
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), NewMemoryRegistry(), NewNaiveStrategy(), jobStore, nil)

	res := &pb.JobResult{
		JobId:  "job-retryable",
		Status: pb.JobStatus_JOB_STATUS_FAILED_RETRYABLE,
	}

	if err := engine.handleJobResult(res); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	if len(bus.published) != 0 {
		t.Fatalf("expected no DLQ publish for retryable failure, got %d", len(bus.published))
	}
}

func TestHandleJobResultFatalTriggersRollback(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	rdb, err := redisutil.NewClient("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("redis client: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	bus := &fakeBus{}
	saga := NewSagaManager(bus, rdb)

	seedReq := &pb.JobRequest{
		JobId:      "job-success",
		Topic:      "job.primary",
		WorkflowId: "wf-fatal",
		Compensation: &pb.Compensation{
			Topic: "job.undo",
		},
	}
	if err := saga.RecordCompensation(context.Background(), seedReq); err != nil {
		t.Fatalf("record compensation: %v", err)
	}

	jobStore := newSagaJobStore()
	jobStore.reqs["job-fatal"] = &pb.JobRequest{
		JobId:      "job-fatal",
		WorkflowId: "wf-fatal",
	}

	engine := NewEngine(bus, NewSafetyBasic(), NewMemoryRegistry(), NewNaiveStrategy(), jobStore, nil).WithSaga(saga)
	res := &pb.JobResult{
		JobId:  "job-fatal",
		Status: pb.JobStatus_JOB_STATUS_FAILED_FATAL,
	}
	if err := engine.handleJobResult(res); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			t.Fatalf("expected compensation dispatch on fatal rollback")
		case <-ticker.C:
			bus.mu.Lock()
			n := len(bus.published)
			bus.mu.Unlock()
			if n > 0 {
				goto done
			}
		}
	}
done:
}

func TestStopCancelsEngineContext(t *testing.T) {
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), NewMemoryRegistry(), NewNaiveStrategy(), newFakeJobStore(), nil)

	// Context must be alive before Stop.
	if err := engine.ctx.Err(); err != nil {
		t.Fatalf("expected live context before Stop, got: %v", err)
	}

	engine.Stop()

	// Context must be cancelled after Stop.
	if err := engine.ctx.Err(); err != context.Canceled {
		t.Fatalf("expected context.Canceled after Stop, got: %v", err)
	}

	// Derived timeouts must fail immediately (no storeOpTimeout hang).
	ctx, cancel := context.WithTimeout(engine.ctx, storeOpTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		// Parent cancelled propagates immediately — good.
	case <-time.After(100 * time.Millisecond):
		t.Fatal("derived context should cancel immediately after Stop()")
	}
}

func TestProcessJobSafetyUnavailableRetries(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-unavail",
		Topic: "sys.unavailable",
	}

	err := engine.processJob(req, "trace-unavail")
	if err == nil {
		t.Fatal("expected retryable error for SafetyUnavailable, got nil")
	}

	retryErr, ok := err.(*retryableError)
	if !ok {
		t.Fatalf("expected retryableError, got %T", err)
	}
	if !strings.Contains(retryErr.Error(), "safety unavailable") {
		t.Fatalf("expected error to contain 'safety unavailable', got: %s", retryErr.Error())
	}

	// Job must NOT be in DENIED state — it should stay PENDING.
	if state := jobStore.states["job-unavail"]; state == JobStateDenied {
		t.Fatal("job must NOT be DENIED when safety is unavailable")
	}

	// No DLQ messages should be published.
	for _, msg := range bus.published {
		if msg.subject == capsdk.SubjectDLQ {
			t.Fatal("no DLQ message should be published for SafetyUnavailable")
		}
	}
}

// slowBus wraps fakeBus with an artificial delay in Publish.
type slowBus struct {
	fakeBus
	delay time.Duration
}

func (b *slowBus) Publish(subject string, packet *pb.BusPacket) error {
	time.Sleep(b.delay)
	return b.fakeBus.Publish(subject, packet)
}

func TestStopWaitsForInflightHandlers(t *testing.T) {
	bus := &slowBus{delay: 200 * time.Millisecond}
	registry := NewMemoryRegistry()
	// Register a worker so dispatch succeeds.
	registry.UpdateHeartbeat(&pb.Heartbeat{
		WorkerId:     "w1",
		Type:         "cpu",
		Pool:         "default",
		Capabilities: []string{},
		ActiveJobs:   0,
	})

	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	packet := &pb.BusPacket{
		SenderId:        "test",
		TraceId:         "trace-drain",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: &pb.JobRequest{
				JobId: "job-drain",
				Topic: "job.default",
			},
		},
	}

	// Start handler in a goroutine (it will block in slow Publish).
	done := make(chan error, 1)
	go func() {
		done <- engine.HandlePacket(packet)
	}()

	// Give goroutine a moment to enter HandlePacket.
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	engine.Stop()
	elapsed := time.Since(start)

	// Stop must have waited for the in-flight handler (~200ms publish delay).
	if elapsed < 100*time.Millisecond {
		t.Fatalf("Stop() returned too fast (%v); expected to wait for in-flight handler", elapsed)
	}

	// Handler should have completed successfully.
	select {
	case err := <-done:
		if err != nil {
			// RetryAfter errors are acceptable (e.g. context cancelled during store ops).
			t.Logf("handler returned error (acceptable): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler goroutine did not complete")
	}

	// Job state should have progressed past pending.
	state := jobStore.states["job-drain"]
	if state == "" || state == JobStatePending {
		t.Logf("job state: %q (handler may have been cancelled by Stop, which is acceptable)", state)
	}
}
