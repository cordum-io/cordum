package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type publishedMsg struct {
	subject string
	packet  *pb.BusPacket
}

type fakeBus struct {
	published []publishedMsg
}

type fakeConfigProvider struct {
	cfg map[string]any
}

func (f *fakeConfigProvider) Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error) {
	return f.cfg, nil
}

type fakeJobStore struct {
	states   map[string]JobState
	ptrs     map[string]string
	topics   map[string]string
	tenants  map[string]string
	teams    map[string]string
	safety   map[string]SafetyDecisionRecord
	attempts map[string]int
	locks    map[string]time.Time
}

func newFakeJobStore() *fakeJobStore {
	return &fakeJobStore{
		states:   make(map[string]JobState),
		ptrs:     make(map[string]string),
		topics:   make(map[string]string),
		tenants:  make(map[string]string),
		teams:    make(map[string]string),
		safety:   make(map[string]SafetyDecisionRecord),
		attempts: make(map[string]int),
		locks:    make(map[string]time.Time),
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

func (s *fakeJobStore) CancelJob(_ context.Context, jobID string) (JobState, error) {
	state := s.states[jobID]
	if terminalStates[state] {
		return state, nil
	}
	s.states[jobID] = JobStateCancelled
	return JobStateCancelled, nil
}

func (b *fakeBus) Publish(subject string, packet *pb.BusPacket) error {
	b.published = append(b.published, publishedMsg{subject: subject, packet: packet})
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

	engine.HandlePacket(packet)

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

	engine.processJob(req, "trace-123")

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

	engine.handleJobResult(res)

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

	engine.processJob(req, "trace-ec")

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

	engine.processJob(req, "trace-block")

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

	engine.processJob(req, "trace-invalid")

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

	engine.handleJobResult(res)

	if state := jobStore.states["job-1"]; state != JobStateSucceeded {
		t.Fatalf("expected job state SUCCEEDED, got %s", state)
	}
	if ptr := jobStore.ptrs["job-1"]; ptr != "redis://res:job-1" {
		t.Fatalf("expected result ptr redis://res:job-1, got %s", ptr)
	}
}
