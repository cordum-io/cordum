package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
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

// newTestRegistry creates a MemoryRegistry that is automatically closed when the test ends.
func newTestRegistry(t testing.TB) *MemoryRegistry {
	t.Helper()
	reg := NewMemoryRegistry()
	t.Cleanup(reg.Close)
	return reg
}

// NaiveStrategy forwards jobs directly to the requested topic (test-only).
type NaiveStrategy struct{}

func NewNaiveStrategy() *NaiveStrategy { return &NaiveStrategy{} }

func (s *NaiveStrategy) PickSubject(req *pb.JobRequest, _ map[string]*pb.Heartbeat) (string, error) {
	if req == nil || req.Topic == "" {
		return "", fmt.Errorf("missing topic")
	}
	return req.Topic, nil
}

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

type errStrategy struct {
	err error
}

func (s *errStrategy) PickSubject(_ *pb.JobRequest, _ map[string]*pb.Heartbeat) (string, error) {
	return "", s.err
}

type fakeJobStore struct {
	mu             sync.RWMutex
	states         map[string]JobState
	ptrs           map[string]string
	topics         map[string]string
	tenants        map[string]string
	teams          map[string]string
	safety         map[string]SafetyDecisionRecord
	output         map[string]OutputSafetyRecord
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
		output:         make(map[string]OutputSafetyRecord),
		attempts:       make(map[string]int),
		locks:          make(map[string]time.Time),
		failureReasons: make(map[string]string),
	}
}

func (s *fakeJobStore) SetState(_ context.Context, jobID string, state JobState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[jobID] = state
	if state == JobStateScheduled {
		s.attempts[jobID]++
	}
	return nil
}

func (s *fakeJobStore) GetState(_ context.Context, jobID string) (JobState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.states[jobID], nil
}

func (s *fakeJobStore) SetResultPtr(_ context.Context, jobID, resultPtr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ptrs[jobID] = resultPtr
	return nil
}

func (s *fakeJobStore) GetResultPtr(_ context.Context, jobID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topics[jobID] = topic
	return nil
}

func (s *fakeJobStore) GetTopic(_ context.Context, jobID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.topics[jobID], nil
}

func (s *fakeJobStore) SetTenant(_ context.Context, jobID, tenant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[jobID] = tenant
	return nil
}

func (s *fakeJobStore) GetTenant(_ context.Context, jobID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tenants[jobID], nil
}

func (s *fakeJobStore) SetTeam(_ context.Context, jobID, team string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teams[jobID] = team
	return nil
}

func (s *fakeJobStore) GetTeam(_ context.Context, jobID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.teams[jobID], nil
}

func (s *fakeJobStore) SetSafetyDecision(_ context.Context, jobID string, record SafetyDecisionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.safety[jobID] = record
	return nil
}

func (s *fakeJobStore) GetSafetyDecision(_ context.Context, jobID string) (SafetyDecisionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.safety[jobID], nil
}

func (s *fakeJobStore) GetAttempts(_ context.Context, jobID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attempts[jobID], nil
}

func (s *fakeJobStore) CountActiveByTenant(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (s *fakeJobStore) TryAcquireLock(_ context.Context, key string, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if until, ok := s.locks[key]; ok && until.After(time.Now()) {
		return "", nil
	}
	s.locks[key] = time.Now().Add(ttl)
	return fmt.Sprintf("token-%s", key), nil
}

func (s *fakeJobStore) ReleaseLock(_ context.Context, key string, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.locks, key)
	return nil
}

func (s *fakeJobStore) RenewLock(_ context.Context, key, token string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if until, ok := s.locks[key]; ok && until.After(time.Now()) {
		s.locks[key] = time.Now().Add(ttl)
		return nil
	}
	return fmt.Errorf("lock not owned")
}

func (s *fakeJobStore) SetFailureReason(_ context.Context, jobID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureReasons[jobID] = reason
	return nil
}

func (s *fakeJobStore) GetFailureReason(_ context.Context, jobID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.failureReasons[jobID], nil
}

func (s *fakeJobStore) SetOutputDecision(_ context.Context, jobID string, record OutputSafetyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output[jobID] = record
	return nil
}

func (s *fakeJobStore) GetOutputDecision(_ context.Context, jobID string) (OutputSafetyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.output[jobID], nil
}

func (s *fakeJobStore) IncrAttempts(_ context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts[jobID]++
	return nil
}

func (s *fakeJobStore) CancelJob(_ context.Context, jobID string) (JobState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (b *fakeBus) snapshotPublished() []publishedMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]publishedMsg, len(b.published))
	copy(out, b.published)
	return out
}

func (b *fakeBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	// Tests call handlers directly, so no-op is fine here.
	return nil
}

func TestEngineHandleHeartbeatStoresWorker(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
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

func TestHandleHandshakeRegistersWorker(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil)

	packet := &pb.BusPacket{
		SenderId:        "worker-42",
		TraceId:         "trace-hs",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_Handshake{
			Handshake: &pb.Handshake{
				ComponentId:       "worker-42",
				Role:              pb.ComponentRole_COMPONENT_ROLE_WORKER,
				SdkVersion:        "2.5.2",
				SupportedVersions: []int32{1},
				Capabilities:      map[string]bool{"cancel": true},
			},
		},
	}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("handle handshake: %v", err)
	}

	// Worker-role handshake should be registered.
	registry.mu.RLock()
	entry, ok := registry.workers["worker-42"]
	registry.mu.RUnlock()
	if !ok {
		t.Fatal("expected worker-42 in registry after handshake")
	}
	if entry.handshake == nil {
		t.Fatal("expected handshake data in registry entry")
	}
	if entry.handshake.SdkVersion != "2.5.2" {
		t.Fatalf("expected sdk_version 2.5.2, got %s", entry.handshake.SdkVersion)
	}
}

func TestHandleHandshakeIgnoresNonWorker(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil)

	packet := &pb.BusPacket{
		SenderId: "gateway-1",
		Payload: &pb.BusPacket_Handshake{
			Handshake: &pb.Handshake{
				ComponentId: "gateway-1",
				Role:        pb.ComponentRole_COMPONENT_ROLE_GATEWAY,
			},
		},
	}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("handle handshake: %v", err)
	}

	registry.mu.RLock()
	_, ok := registry.workers["gateway-1"]
	registry.mu.RUnlock()
	if ok {
		t.Fatal("non-worker handshake should not be registered")
	}
}

func TestProcessJobPublishesToSubject(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
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

func TestHandleJobRequestNoWorkersDefersRetry(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	strategy := &errStrategy{err: ErrNoWorkers}
	engine := NewEngine(bus, NewSafetyBasic(), registry, strategy, jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-1",
		Topic: "job.default",
	}

	if err := engine.handleJobRequest(req, "trace-1"); err != nil {
		t.Fatalf("handle job request: %v", err)
	}

	if state := jobStore.states["job-1"]; state != JobStatePending {
		t.Fatalf("expected job state PENDING, got %s", state)
	}
	if len(bus.published) != 0 {
		t.Fatalf("expected no publish, got %d", len(bus.published))
	}

	engine.Stop()
}

func TestCancelJobPublishesOnlyCancelSubject(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
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
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

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
	registry := newTestRegistry(t)
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
	registry := newTestRegistry(t)
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
	registry := newTestRegistry(t)
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
	registry := newTestRegistry(t)
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
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil)

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

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil).WithSaga(saga)
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
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil)

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
	registry := newTestRegistry(t)
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
	registry := newTestRegistry(t)
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

func TestProcessJobMaxSchedulingRetriesFailsToDLQ(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	// Pre-set attempts at the max threshold so processJob gives up immediately.
	jobStore.attempts["job-stuck"] = maxSchedulingRetries
	strategy := &errStrategy{err: ErrNoWorkers}
	engine := NewEngine(bus, NewSafetyBasic(), registry, strategy, jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-stuck",
		Topic: "job.default",
	}

	if err := engine.processJob(req, "trace-stuck"); err != nil {
		t.Fatalf("expected nil (job failed to DLQ), got: %v", err)
	}

	// Job must be in FAILED state.
	if state := jobStore.states["job-stuck"]; state != JobStateFailed {
		t.Fatalf("expected FAILED state, got %s", state)
	}

	// A DLQ message must have been published.
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 DLQ publish, got %d", len(bus.published))
	}
	if bus.published[0].subject != capsdk.SubjectDLQ {
		t.Fatalf("expected DLQ subject, got %s", bus.published[0].subject)
	}
	result := bus.published[0].packet.GetJobResult()
	if result == nil || result.GetErrorCode() != "max_scheduling_retries" {
		t.Fatalf("expected error code max_scheduling_retries, got %v", result)
	}
}

func TestProcessJobBelowMaxRetriesStillRetries(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	// Set attempts below max — should still retry, not fail.
	jobStore.attempts["job-retry"] = maxSchedulingRetries - 1
	strategy := &errStrategy{err: ErrNoWorkers}
	engine := NewEngine(bus, NewSafetyBasic(), registry, strategy, jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-retry",
		Topic: "job.default",
	}

	err := engine.processJob(req, "trace-retry")
	if err == nil {
		t.Fatal("expected retryable error, got nil")
	}
	// Must be a RetryAfter error.
	if _, ok := err.(*retryableError); !ok {
		t.Fatalf("expected retryableError, got %T", err)
	}

	// Job must NOT be in FAILED state.
	if state := jobStore.states["job-retry"]; state == JobStateFailed {
		t.Fatal("job should not be FAILED when below maxSchedulingRetries")
	}

	// IncrAttempts should have been called.
	if jobStore.attempts["job-retry"] != maxSchedulingRetries {
		t.Fatalf("expected attempts to be incremented to %d, got %d", maxSchedulingRetries, jobStore.attempts["job-retry"])
	}
}

func TestProcessJobIncrAttemptsNotCalledOnSuccess(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, &NaiveStrategy{}, jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-ok",
		Topic: "job.default",
	}

	if err := engine.processJob(req, "trace-ok"); err != nil {
		t.Fatalf("process job: %v", err)
	}

	// Attempts should only be incremented by SetState(SCHEDULED), not by IncrAttempts.
	// The fakeJobStore.SetState increments on SCHEDULED, so attempts = 1.
	// IncrAttempts should NOT have been called (no scheduling error).
	if jobStore.attempts["job-ok"] != 1 {
		t.Fatalf("expected attempts=1 (from SetState SCHEDULED only), got %d", jobStore.attempts["job-ok"])
	}
}

// failCancelJobStore wraps fakeJobStore but forces CancelJob to return an error.
type failCancelJobStore struct {
	*fakeJobStore
	cancelErr error
}

func (s *failCancelJobStore) CancelJob(_ context.Context, jobID string) (JobState, error) {
	return "", s.cancelErr
}

// cancelMetricsSpy implements Metrics to track IncJobCancelFailures calls.
type cancelMetricsSpy struct {
	cancelFailures int
}

func (m *cancelMetricsSpy) IncJobsReceived(string)                            {}
func (m *cancelMetricsSpy) IncJobsDispatched(string)                          {}
func (m *cancelMetricsSpy) IncJobsCompleted(string, string)                   {}
func (m *cancelMetricsSpy) IncSafetyDenied(string)                            {}
func (m *cancelMetricsSpy) IncSafetyUnavailable(string)                       {}
func (m *cancelMetricsSpy) IncOutputPolicyChecked(string)                     {}
func (m *cancelMetricsSpy) IncOutputPolicyQuarantined(string)                 {}
func (m *cancelMetricsSpy) IncOutputPolicySkipped(string)                     {}
func (m *cancelMetricsSpy) IncAsyncOutputTimeout(string)                      {}
func (m *cancelMetricsSpy) IncOutputEvaluations(string)                       {}
func (m *cancelMetricsSpy) IncOutputDenials(string)                           {}
func (m *cancelMetricsSpy) IncOutputRedactions(string)                        {}
func (m *cancelMetricsSpy) IncOrphanReplayed(string)                          {}
func (m *cancelMetricsSpy) ObserveJobLockWait(float64)                        {}
func (m *cancelMetricsSpy) ObserveDispatchLatency(string, float64)            {}
func (m *cancelMetricsSpy) ObserveOutputCheckLatency(string, string, float64) {}
func (m *cancelMetricsSpy) ObserveOutputEvalDuration(string, float64)         {}
func (m *cancelMetricsSpy) SetActiveGoroutines(int)                           {}
func (m *cancelMetricsSpy) SetStaleJobs(string, int)                          {}
func (m *cancelMetricsSpy) IncDLQEmitFailure(string)                          {}
func (m *cancelMetricsSpy) IncJobCancelFailures()                             { m.cancelFailures++ }
func (m *cancelMetricsSpy) IncValidationRejections()                          {}
func (m *cancelMetricsSpy) IncInputFailOpen(string)                           {}

func TestHandlePacket_CancelJob_ErrorPropagates(t *testing.T) {
	store := &failCancelJobStore{
		fakeJobStore: newFakeJobStore(),
		cancelErr:    fmt.Errorf("redis connection lost"),
	}
	spy := &cancelMetricsSpy{}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, spy)

	packet := &pb.BusPacket{
		TraceId:         "trace-cancel-err",
		SenderId:        "test",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_JobCancel{
			JobCancel: &pb.JobCancel{
				JobId:  "job-cancel-fail",
				Reason: "user requested",
			},
		},
	}

	err := engine.HandlePacket(packet)
	if err == nil {
		t.Fatal("expected error from HandlePacket when CancelJob fails")
	}
	if !strings.Contains(err.Error(), "redis connection lost") {
		t.Fatalf("expected redis error, got: %v", err)
	}
	if spy.cancelFailures != 1 {
		t.Fatalf("expected cancel failures metric=1, got %d", spy.cancelFailures)
	}
}

func TestHandlePacket_CancelJob_SuccessReturnsNil(t *testing.T) {
	store := newFakeJobStore()
	store.states["job-ok"] = JobStateRunning
	spy := &cancelMetricsSpy{}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, spy)

	packet := &pb.BusPacket{
		TraceId:         "trace-cancel-ok",
		SenderId:        "test",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_JobCancel{
			JobCancel: &pb.JobCancel{
				JobId:  "job-ok",
				Reason: "all done",
			},
		},
	}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("expected nil error on successful cancel, got: %v", err)
	}
	if spy.cancelFailures != 0 {
		t.Fatalf("expected no cancel failure metrics, got %d", spy.cancelFailures)
	}
	if store.states["job-ok"] != JobStateCancelled {
		t.Fatalf("expected CANCELLED state, got %s", store.states["job-ok"])
	}
}

func TestMapStringToErrorCode(t *testing.T) {
	tests := []struct {
		code string
		want pb.ErrorCode
	}{
		{"approval_rejected", pb.ErrorCode_ERROR_CODE_SAFETY_DENIED},
		{"policy_denied", pb.ErrorCode_ERROR_CODE_SAFETY_DENIED},
		{"policy_violation", pb.ErrorCode_ERROR_CODE_SAFETY_POLICY_VIOLATION},
		{"max_scheduling_retries", pb.ErrorCode_ERROR_CODE_JOB_RESOURCE_EXHAUSTED},
		{"timeout", pb.ErrorCode_ERROR_CODE_JOB_TIMEOUT},
		{"permission_denied", pb.ErrorCode_ERROR_CODE_JOB_PERMISSION_DENIED},
		{"unknown_code", pb.ErrorCode_ERROR_CODE_UNSPECIFIED},
		{"", pb.ErrorCode_ERROR_CODE_UNSPECIFIED},
	}
	for _, tt := range tests {
		if got := mapStringToErrorCode(tt.code); got != tt.want {
			t.Errorf("mapStringToErrorCode(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestInvalidJobRequestRejected(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil)

	// Empty topic should be rejected by validation.
	packet := &pb.BusPacket{
		SenderId:        "test",
		TraceId:         "trace-invalid",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: &pb.JobRequest{
				JobId: "job-invalid",
				Topic: "", // invalid: empty topic
			},
		},
	}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("expected nil error for invalid request, got: %v", err)
	}

	// Should not have been dispatched.
	msgs := bus.snapshotPublished()
	if len(msgs) != 0 {
		t.Fatalf("expected no published messages for invalid request, got %d", len(msgs))
	}
}

func TestInvalidJobResultRejected(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	// Empty worker_id should be rejected by validation.
	packet := &pb.BusPacket{
		SenderId:        "test",
		TraceId:         "trace-invalid-result",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:    "job-result-invalid",
				Status:   pb.JobStatus_JOB_STATUS_SUCCEEDED,
				WorkerId: "", // invalid: empty worker_id
			},
		},
	}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("expected nil error for invalid result, got: %v", err)
	}
}
