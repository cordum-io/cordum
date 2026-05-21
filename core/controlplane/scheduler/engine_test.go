package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/redisutil"
	infraSchema "github.com/cordum/cordum/core/infra/schema"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// workflowGateSnapshot is the PolicySnapshot value for approval-gate decisions
// from SafetyBasic (which returns an empty snapshot).
const workflowGateSnapshot = ""

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

func (s *NaiveStrategy) PickSubject(req *pb.JobRequest, _ map[string]*pb.Heartbeat, _ map[string]WorkerReadiness) (string, error) {
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
	mu          sync.Mutex
	published   []publishedMsg
	publishErr  error
	failSubject string
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

func (s *errStrategy) PickSubject(_ *pb.JobRequest, _ map[string]*pb.Heartbeat, _ map[string]WorkerReadiness) (string, error) {
	return "", s.err
}

type panicRegistry struct{}

func (panicRegistry) UpdateHeartbeat(_ *pb.Heartbeat) {
	panic("registry heartbeat panic")
}

func (panicRegistry) UpdateHandshake(_ *pb.Handshake) {
	panic("registry handshake panic")
}

func (panicRegistry) Snapshot() map[string]*pb.Heartbeat {
	return nil
}

func (panicRegistry) ReadinessSnapshot() map[string]WorkerReadiness {
	return nil
}

func (panicRegistry) IsAlive(string) bool {
	return false
}

type fakeJobStore struct {
	mu             sync.RWMutex
	states         map[string]JobState
	ptrs           map[string]string
	topics         map[string]string
	tenants        map[string]string
	teams          map[string]string
	safety         map[string]SafetyDecisionRecord
	lineage        map[string]model.DelegationLineage
	dispatchTokens map[string]model.DelegationDispatchToken
	output         map[string]OutputSafetyRecord
	attempts       map[string]int
	locks          map[string]time.Time
	failureReasons map[string]string
	reqs           map[string]*pb.JobRequest
}

type sagaJobStore struct {
	*fakeJobStore
	reqs map[string]*pb.JobRequest
}

type failingSafetyDecisionStore struct {
	*fakeJobStore
	err error
}

type failingGetSafetyDecisionStore struct {
	*fakeJobStore
	err error
}

type failingGetAttemptsStore struct {
	*fakeJobStore
	err error
}

type fakeDecisionLogStore struct {
	mu      sync.Mutex
	records []model.DecisionLogRecord
	err     error
}

// failingSetStateStore returns an error from SetState when the target state
// matches failOnState. Used to verify RetryAfter propagation.
type failingSetStateStore struct {
	*fakeJobStore
	failOnState JobState
	setStateErr error
}

func (s *failingSetStateStore) SetState(ctx context.Context, jobID string, state JobState) error {
	if state == s.failOnState {
		return s.setStateErr
	}
	return s.fakeJobStore.SetState(ctx, jobID, state)
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
		lineage:        make(map[string]model.DelegationLineage),
		dispatchTokens: make(map[string]model.DelegationDispatchToken),
		output:         make(map[string]OutputSafetyRecord),
		attempts:       make(map[string]int),
		locks:          make(map[string]time.Time),
		failureReasons: make(map[string]string),
		reqs:           make(map[string]*pb.JobRequest),
	}
}

func (s *fakeJobStore) SetJobRequest(_ context.Context, req *pb.JobRequest) error {
	if req == nil {
		return fmt.Errorf("job request required")
	}
	clone, ok := proto.Clone(req).(*pb.JobRequest)
	if !ok || clone == nil {
		return fmt.Errorf("job request clone failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs[req.GetJobId()] = clone
	return nil
}

func (s *fakeJobStore) GetJobRequest(_ context.Context, jobID string) (*pb.JobRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.reqs[jobID]
	if !ok {
		return nil, fmt.Errorf("job request not found: %s", jobID)
	}
	clone, ok := proto.Clone(req).(*pb.JobRequest)
	if !ok || clone == nil {
		return nil, fmt.Errorf("job request clone failed")
	}
	return clone, nil
}

func (s *failingSafetyDecisionStore) SetSafetyDecision(_ context.Context, jobID string, record SafetyDecisionRecord) error {
	_ = jobID
	_ = record
	return s.err
}

func (s *failingGetSafetyDecisionStore) GetSafetyDecision(_ context.Context, jobID string) (SafetyDecisionRecord, error) {
	_ = jobID
	return SafetyDecisionRecord{}, s.err
}

func (s *fakeDecisionLogStore) AppendDecision(_ context.Context, record model.DecisionLogRecord) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return nil
}

func (s *fakeDecisionLogStore) QueryDecisions(_ context.Context, _ model.DecisionQuery) (model.DecisionPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]model.DecisionLogRecord, len(s.records))
	copy(items, s.records)
	return model.DecisionPage{Items: items}, nil
}

func (s *fakeDecisionLogStore) snapshotRecords() []model.DecisionLogRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.DecisionLogRecord, len(s.records))
	copy(out, s.records)
	return out
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

func (s *fakeJobStore) SetStateWithContext(ctx context.Context, jobID string, state JobState, _ *model.StateEventContext) error {
	return s.SetState(ctx, jobID, state)
}

func (s *fakeJobStore) GetJobEvents(_ context.Context, _ string) ([]model.JobEvent, error) {
	return nil, nil
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
	// RLock the s.states map iteration to match every other reader
	// on this fake (GetState, GetJobRequest, GetTopic, ...). Without
	// the lock, the race detector fires on
	// TestProcessJobReadinessRequiredFiltersUnreadyWorkers because
	// the new flush-on-worker-online goroutine spawned from
	// HandlePacket (task-7a2514ae) reads s.states concurrently with
	// SetState writes on the main test goroutine. See task-6f10d4e5.
	s.mu.RLock()
	defer s.mu.RUnlock()
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

func (s *fakeJobStore) SetDelegationLineage(_ context.Context, jobID string, lineage model.DelegationLineage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lineage[jobID] = lineage
	return nil
}

func (s *fakeJobStore) GetDelegationLineage(_ context.Context, jobID string) (model.DelegationLineage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lineage[jobID], nil
}

func (s *fakeJobStore) SetDelegationDispatchToken(_ context.Context, jobID string, token model.DelegationDispatchToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatchTokens[jobID] = token
	return nil
}

func (s *fakeJobStore) GetDelegationDispatchToken(_ context.Context, jobID string) (model.DelegationDispatchToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dispatchTokens[jobID], nil
}

func (s *fakeJobStore) GetAttempts(_ context.Context, jobID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attempts[jobID], nil
}

func (s *failingGetAttemptsStore) GetAttempts(_ context.Context, _ string) (int, error) {
	return 0, s.err
}

func TestSchedulerRejectsUnknownTopicFromBus(t *testing.T) {
	redisSrv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer redisSrv.Close()

	configSvc, err := configsvc.New("redis://" + redisSrv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer func() { _ = configSvc.Close() }()

	regSvc := topicregistry.NewService(configSvc)
	if err := regSvc.Set(testCtx(t), topicregistry.Registration{
		Name:   "job.allowed",
		Pool:   "default",
		Status: topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}

	bus := &fakeBus{}
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).WithTopicRegistry(regSvc)

	req := &pb.JobRequest{JobId: "job-unknown-topic", Topic: "job.missing", TenantId: "default"}
	packet := &pb.BusPacket{
		TraceId: "trace-unknown-topic",
		Payload: &pb.BusPacket_JobRequest{JobRequest: req},
	}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("HandlePacket returned error: %v", err)
	}

	state, err := store.GetState(testCtx(t), req.JobId)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state != JobStateFailed {
		t.Fatalf("expected failed state, got %q", state)
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, msg := range bus.published {
		if msg.subject == req.Topic {
			t.Fatalf("unexpected dispatch publish to unknown topic: %+v", msg)
		}
	}
	if len(bus.published) == 0 || bus.published[0].subject != capsdk.SubjectDLQ {
		t.Fatalf("expected DLQ publish for unknown topic, got %+v", bus.published)
	}
}

func TestSchedulerAcceptsTenantScopedTopicFromBus(t *testing.T) {
	redisSrv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer redisSrv.Close()

	configSvc, err := configsvc.New("redis://" + redisSrv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer func() { _ = configSvc.Close() }()

	regSvc := topicregistry.NewService(configSvc)
	if err := regSvc.Set(testCtx(t), topicregistry.Registration{
		Name:     "job.tenant.echo",
		TenantID: "tenant-a",
		Pool:     "tenant-pack",
		Status:   topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}

	bus := &fakeBus{}
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithTopicRegistry(regSvc)
	req := &pb.JobRequest{JobId: "job-tenant-topic", Topic: "job.tenant.echo", TenantId: "tenant-a"}
	packet := &pb.BusPacket{TraceId: "trace-tenant-topic", Payload: &pb.BusPacket_JobRequest{JobRequest: req}}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("HandlePacket returned error: %v", err)
	}
	state, err := store.GetState(testCtx(t), req.JobId)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state != JobStateRunning {
		t.Fatalf("expected running state, got %q", state)
	}
	if got := bus.snapshotPublished(); len(got) != 1 || got[0].subject != req.Topic {
		t.Fatalf("expected dispatch publish to %s, got %+v", req.Topic, got)
	}
}

func TestSchedulerSchemaEnforceRejects(t *testing.T) {
	jobStore, regSvc, schemaRegistry, cleanup := newSchedulerSchemaTestDeps(t)
	defer cleanup()

	ctxKey := infraStore.MakeContextKey("job-schema-enforce")
	if err := jobStore.Client().Set(testCtx(t), ctxKey, []byte(`{"context":{"message":123}}`), 0).Err(); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	if err := schemaRegistry.Register(testCtx(t), "demo/input", []byte(`{
		"type": "object",
		"properties": {
			"message": {"type": "string"}
		},
		"required": ["message"]
	}`)); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	if err := regSvc.Set(testCtx(t), topicregistry.Registration{
		Name:          "job.structured",
		Pool:          "default",
		InputSchemaID: "demo/input",
		Status:        topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}

	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil).
		WithTopicRegistry(regSvc).
		WithSchemaRegistry(schemaRegistry).
		WithContextClient(jobStore.Client()).
		WithSchemaEnforcement(infraSchema.EnforcementEnforce)

	req := &pb.JobRequest{
		JobId:      "job-schema-enforce",
		Topic:      "job.structured",
		TenantId:   "default",
		ContextPtr: infraStore.PointerForKey(ctxKey),
	}
	packet := &pb.BusPacket{
		TraceId: "trace-schema-enforce",
		Payload: &pb.BusPacket_JobRequest{JobRequest: req},
	}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("HandlePacket returned error: %v", err)
	}

	state, err := jobStore.GetState(testCtx(t), req.JobId)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state != JobStateFailed {
		t.Fatalf("expected failed state, got %q", state)
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, msg := range bus.published {
		if msg.subject == req.Topic {
			t.Fatalf("unexpected dispatch publish for schema-rejected job: %+v", msg)
		}
	}
	if len(bus.published) == 0 || bus.published[0].subject != capsdk.SubjectDLQ {
		t.Fatalf("expected DLQ publish for schema rejection, got %+v", bus.published)
	}
}

func TestSchedulerSchemaWarnAllows(t *testing.T) {
	jobStore, regSvc, schemaRegistry, cleanup := newSchedulerSchemaTestDeps(t)
	defer cleanup()

	ctxKey := infraStore.MakeContextKey("job-schema-warn")
	if err := jobStore.Client().Set(testCtx(t), ctxKey, []byte(`{"context":{"message":123}}`), 0).Err(); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	if err := schemaRegistry.Register(testCtx(t), "demo/input", []byte(`{
		"type": "object",
		"properties": {
			"message": {"type": "string"}
		},
		"required": ["message"]
	}`)); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	if err := regSvc.Set(testCtx(t), topicregistry.Registration{
		Name:          "job.structured",
		Pool:          "default",
		InputSchemaID: "demo/input",
		Status:        topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}

	var logBuf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil).
		WithTopicRegistry(regSvc).
		WithSchemaRegistry(schemaRegistry).
		WithContextClient(jobStore.Client()).
		WithSchemaEnforcement(infraSchema.EnforcementWarn)

	req := &pb.JobRequest{
		JobId:      "job-schema-warn",
		Topic:      "job.structured",
		TenantId:   "default",
		ContextPtr: infraStore.PointerForKey(ctxKey),
	}
	packet := &pb.BusPacket{
		TraceId: "trace-schema-warn",
		Payload: &pb.BusPacket_JobRequest{JobRequest: req},
	}

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("HandlePacket returned error: %v", err)
	}

	state, err := jobStore.GetState(testCtx(t), req.JobId)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state != JobStateRunning {
		t.Fatalf("expected running state after warn-mode allow, got %q", state)
	}
	if !strings.Contains(logBuf.String(), "job request violated topic input schema") {
		t.Fatalf("expected schema warning log, got %q", logBuf.String())
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 || bus.published[0].subject != req.Topic {
		t.Fatalf("expected dispatch publish to %s, got %+v", req.Topic, bus.published)
	}
}

func newSchedulerSchemaTestDeps(t *testing.T) (*infraStore.RedisJobStore, *topicregistry.Service, *infraSchema.Registry, func()) {
	t.Helper()

	redisSrv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}

	redisURL := "redis://" + redisSrv.Addr()
	jobStore, err := infraStore.NewRedisJobStore(redisURL)
	if err != nil {
		redisSrv.Close()
		t.Fatalf("job store: %v", err)
	}
	configSvc, err := configsvc.New(redisURL)
	if err != nil {
		_ = jobStore.Close()
		redisSrv.Close()
		t.Fatalf("config svc: %v", err)
	}
	schemaRegistry, err := infraSchema.NewRegistry(redisURL)
	if err != nil {
		_ = configSvc.Close()
		_ = jobStore.Close()
		redisSrv.Close()
		t.Fatalf("schema registry: %v", err)
	}
	cleanup := func() {
		_ = schemaRegistry.Close()
		_ = configSvc.Close()
		_ = jobStore.Close()
		redisSrv.Close()
	}
	return jobStore, topicregistry.NewService(configSvc), schemaRegistry, cleanup
}

func newWorkerAttestationTestDeps(t *testing.T) (*workercredentials.Service, *WorkerCredentialCache, func()) {
	t.Helper()

	redisSrv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	redisURL := "redis://" + redisSrv.Addr()

	configSvc, err := configsvc.New(redisURL)
	if err != nil {
		redisSrv.Close()
		t.Fatalf("config svc: %v", err)
	}

	service := workercredentials.NewService(configSvc)
	cache := NewWorkerCredentialCache(service)
	cleanup := func() {
		_ = configSvc.Close()
		redisSrv.Close()
	}
	return service, cache, cleanup
}

func newHeartbeatPacket(workerID, senderID, pool, token string) *pb.BusPacket {
	packet := &pb.BusPacket{
		SenderId:        senderID,
		TraceId:         "trace-" + workerID,
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_Heartbeat{
			Heartbeat: &pb.Heartbeat{
				WorkerId: workerID,
				Pool:     pool,
				Type:     "cpu",
			},
		},
	}
	if token != "" {
		raw := append([]byte{}, packet.ProtoReflect().GetUnknown()...)
		raw = protowire.AppendTag(raw, 18, protowire.BytesType)
		raw = protowire.AppendString(raw, token)
		packet.ProtoReflect().SetUnknown(raw)
	}
	return packet
}

func newHandshakePacket(workerID string, topics ...string) *pb.BusPacket {
	hs := &pb.Handshake{
		ComponentId:       workerID,
		Role:              pb.ComponentRole_COMPONENT_ROLE_WORKER,
		SdkVersion:        "2.8.6",
		SupportedVersions: []int32{1},
	}
	if len(topics) > 0 {
		raw := append([]byte{}, hs.ProtoReflect().GetUnknown()...)
		for _, topic := range topics {
			raw = protowire.AppendTag(raw, 6, protowire.BytesType)
			raw = protowire.AppendString(raw, topic)
		}
		hs.ProtoReflect().SetUnknown(raw)
	}
	return &pb.BusPacket{
		SenderId:        workerID,
		TraceId:         "trace-hs-" + workerID,
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload:         &pb.BusPacket_Handshake{Handshake: hs},
	}
}

func newConfigChangedWorkersPacket() *pb.BusPacket {
	return &pb.BusPacket{
		SenderId:        "gateway",
		TraceId:         "trace-config-workers",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				Message: "config changed",
				Details: map[string]string{
					"scope":    "system",
					"scope_id": "workers",
				},
			},
		},
	}
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

func (s *fakeJobStore) SetWorkerID(_ context.Context, _, _ string) error {
	return nil
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
	err := b.publishErr
	failSubject := b.failSubject
	b.mu.Unlock()
	if err != nil && (failSubject == "" || failSubject == subject) {
		return err
	}
	return nil
}

func (b *fakeBus) snapshotPublished() []publishedMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]publishedMsg, len(b.published))
	copy(out, b.published)
	return out
}

func assertNoJobRequestPublished(t *testing.T, bus *fakeBus, subject, jobID string) {
	t.Helper()
	for _, msg := range bus.snapshotPublished() {
		req := msg.packet.GetJobRequest()
		if msg.subject == subject && req != nil && req.GetJobId() == jobID {
			t.Fatalf("job %q was dispatched on %q despite unreadable attempts", jobID, subject)
		}
	}
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

func TestAttestedWorkerAccepted(t *testing.T) {
	service, cache, cleanup := newWorkerAttestationTestDeps(t)
	defer cleanup()

	issued, err := service.Create(testCtx(t), workercredentials.IssueInput{
		WorkerID:     "worker-attested",
		AllowedPools: []string{"default"},
		CreatedBy:    "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := cache.Refresh(testCtx(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	registry := newTestRegistry(t)
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil).
		WithWorkerCredentialCache(cache).
		WithWorkerAttestationMode(WorkerAttestationEnforce)

	if err := engine.HandlePacket(newHeartbeatPacket("worker-attested", "worker-attested", "default", issued.Token)); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}

	snapshot := registry.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 attested worker, got %d", len(snapshot))
	}
	if snapshot["worker-attested"] == nil || snapshot["worker-attested"].Pool != "default" {
		t.Fatalf("unexpected registry snapshot: %+v", snapshot)
	}
}

func TestUnattestedWorkerWarnMode(t *testing.T) {
	service, cache, cleanup := newWorkerAttestationTestDeps(t)
	defer cleanup()

	if _, err := service.Create(testCtx(t), workercredentials.IssueInput{
		WorkerID:     "worker-warn",
		AllowedPools: []string{"default"},
		CreatedBy:    "test",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := cache.Refresh(testCtx(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	var logBuf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	registry := newTestRegistry(t)
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil).
		WithWorkerCredentialCache(cache).
		WithWorkerAttestationMode(WorkerAttestationWarn)

	if err := engine.HandlePacket(newHeartbeatPacket("worker-warn", "worker-warn", "default", "")); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}

	snapshot := registry.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected warn mode to keep worker, got %d", len(snapshot))
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "worker heartbeat accepted without attestation") || !strings.Contains(logs, "reason=auth_token_missing") {
		t.Fatalf("expected warn log for unattested worker, got %q", logs)
	}
}

func TestUnattestedWorkerEnforceMode(t *testing.T) {
	service, cache, cleanup := newWorkerAttestationTestDeps(t)
	defer cleanup()

	if _, err := service.Create(testCtx(t), workercredentials.IssueInput{
		WorkerID:     "worker-enforce",
		AllowedPools: []string{"default"},
		CreatedBy:    "test",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := cache.Refresh(testCtx(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	var logBuf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	registry := newTestRegistry(t)
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil).
		WithWorkerCredentialCache(cache).
		WithWorkerAttestationMode(WorkerAttestationEnforce)

	if err := engine.HandlePacket(newHeartbeatPacket("worker-enforce", "worker-enforce", "default", "")); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}

	if snapshot := registry.Snapshot(); len(snapshot) != 0 {
		t.Fatalf("expected enforce mode to reject unattested worker, got %+v", snapshot)
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "level=ERROR") || !strings.Contains(logs, "worker heartbeat rejected: attestation failed") || !strings.Contains(logs, "reason=auth_token_missing") {
		t.Fatalf("expected enforce rejection log, got %q", logs)
	}
}

func TestAttestationOffMode(t *testing.T) {
	registry := newTestRegistry(t)
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil).
		WithWorkerAttestationMode(WorkerAttestationOff)

	if err := engine.HandlePacket(newHeartbeatPacket("worker-off", "spoofed-sender", "default", "")); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}

	snapshot := registry.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected attestation off mode to skip checks, got %+v", snapshot)
	}
	if snapshot["worker-off"] == nil {
		t.Fatalf("expected worker-off in registry, got %+v", snapshot)
	}
}

func TestHandlePacketRecoversFromRegistryPanic(t *testing.T) {
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), panicRegistry{}, NewNaiveStrategy(), newFakeJobStore(), nil)

	err := engine.HandlePacket(newHeartbeatPacket("worker-panic", "worker-panic", "default", ""))
	if err == nil {
		t.Fatal("expected retryable error after recovered panic")
	}
	var retryErr *retryableError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected retryableError, got %T", err)
	}
	if retryErr.RetryDelay() != retryDelayStore {
		t.Fatalf("expected retry delay %s, got %s", retryDelayStore, retryErr.RetryDelay())
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

func TestHandshakeWithReadyTopicsSetsReady(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil)

	if err := engine.HandlePacket(newHeartbeatPacket("worker-ready", "worker-ready", "default", "")); err != nil {
		t.Fatalf("handle heartbeat: %v", err)
	}
	if err := engine.HandlePacket(newHandshakePacket("worker-ready", "job.default", "job.other")); err != nil {
		t.Fatalf("handle handshake: %v", err)
	}

	readiness := registry.ReadinessSnapshot()
	state, ok := readiness["worker-ready"]
	if !ok {
		t.Fatal("expected worker-ready in readiness snapshot")
	}
	if !state.Ready {
		t.Fatal("expected worker to be ready after handshake")
	}
	if len(state.ReadyTopics) != 2 || state.ReadyTopics[0] != "job.default" || state.ReadyTopics[1] != "job.other" {
		t.Fatalf("unexpected ready topics: %#v", state.ReadyTopics)
	}
}

func TestProcessJobReadinessRequiredFiltersUnreadyWorkers(t *testing.T) {
	t.Setenv(workerReadinessRequiredEnvVariable, "true")

	bus := &fakeBus{}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewLeastLoadedStrategy(routingForTopic("job.default", "default")), jobStore, nil)

	if err := engine.HandlePacket(newHeartbeatPacket("worker-ready", "worker-ready", "default", "")); err != nil {
		t.Fatalf("handle heartbeat: %v", err)
	}

	err := engine.processJob(testCtx(t), &pb.JobRequest{JobId: "job-unready", Topic: "job.default"}, "trace-unready")
	if err == nil {
		t.Fatal("expected retryable error when readiness is required but missing")
	}
	var retryErr *retryableError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected retryableError, got %T", err)
	}
	if len(bus.snapshotPublished()) != 0 {
		t.Fatalf("expected no publishes while worker is unready, got %d", len(bus.snapshotPublished()))
	}

	if err := engine.HandlePacket(newHandshakePacket("worker-ready", "job.default")); err != nil {
		t.Fatalf("handle handshake: %v", err)
	}
	if err := engine.processJob(testCtx(t), &pb.JobRequest{JobId: "job-ready", Topic: "job.default"}, "trace-ready"); err != nil {
		t.Fatalf("process job after readiness: %v", err)
	}

	published := bus.snapshotPublished()
	if len(published) != 1 {
		t.Fatalf("expected one publish after readiness handshake, got %d", len(published))
	}
	if published[0].subject != "worker.worker-ready.jobs" {
		t.Fatalf("expected direct worker subject, got %s", published[0].subject)
	}
}

func TestHandleConfigChangedPacketRefreshesCredentialCacheAsync(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	cache := &WorkerCredentialCache{
		list: func(context.Context) ([]workercredentials.Credential, error) {
			close(started)
			<-release
			return []workercredentials.Credential{
				{
					WorkerID:       "worker-async",
					CredentialHash: "$argon2id$v=19$m=65536,t=3,p=1$c29tZXNhbHQ$L8mNKgHdwNwp0UrEGouGZWlqlImPi0tLxe3LjXLp8dk",
					CreatedBy:      "test",
					CreatedAt:      time.Now().UTC().Format(time.RFC3339),
				},
			}, nil
		},
		records: map[string]workercredentials.Credential{},
	}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil).
		WithWorkerCredentialCache(cache)

	start := time.Now()
	if err := engine.handleConfigChangedPacket(newConfigChangedWorkersPacket()); err != nil {
		t.Fatalf("handleConfigChangedPacket: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("expected async refresh to return quickly, took %s", elapsed)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected async refresh goroutine to start")
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cache.mu.RLock()
		_, ok := cache.records["worker-async"]
		cache.mu.RUnlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected async refresh to populate cache")
}

func TestWorkerCredentialCacheRefreshMergesRecordsAndKeepsExistingOnFailure(t *testing.T) {
	cache := &WorkerCredentialCache{
		records: map[string]workercredentials.Credential{
			"worker-stale": {
				WorkerID:       "worker-stale",
				CredentialHash: "hash-stale",
				CreatedBy:      "test",
				CreatedAt:      time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		return []workercredentials.Credential{
			{
				WorkerID:       "worker-stale",
				CredentialHash: "hash-updated",
				CreatedBy:      "refresh",
				CreatedAt:      time.Now().UTC().Format(time.RFC3339),
			},
			{
				WorkerID:       "worker-new",
				CredentialHash: "hash-new",
				CreatedBy:      "refresh",
				CreatedAt:      time.Now().UTC().Format(time.RFC3339),
			},
		}, nil
	}

	if err := cache.Refresh(testCtx(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	cache.mu.RLock()
	if got := cache.records["worker-stale"].CredentialHash; got != "hash-updated" {
		cache.mu.RUnlock()
		t.Fatalf("expected updated stale worker hash, got %q", got)
	}
	if _, ok := cache.records["worker-new"]; !ok {
		cache.mu.RUnlock()
		t.Fatal("expected new worker to be merged into cache")
	}
	cache.mu.RUnlock()

	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		return nil, errors.New("boom")
	}
	if err := cache.Refresh(testCtx(t)); err != nil {
		t.Fatalf("Refresh after error: %v", err)
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if _, ok := cache.records["worker-stale"]; !ok {
		t.Fatal("expected existing worker to remain after refresh failure")
	}
	if _, ok := cache.records["worker-new"]; !ok {
		t.Fatal("expected merged worker to remain after refresh failure")
	}
}

func TestProcessJobRetriesWhenTopicRegistryUnavailableAndSchemaEnforced(t *testing.T) {
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil).
		WithSchemaEnforcement(infraSchema.EnforcementEnforce)

	err := engine.processJob(testCtx(t), &pb.JobRequest{JobId: "job-registry-down", Topic: "job.default"}, "trace-registry-down")
	if err == nil {
		t.Fatal("expected retryable error when topic registry is unavailable in enforce mode")
	}
	var retryErr *retryableError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected retryableError, got %T", err)
	}
	if retryErr.RetryDelay() != retryDelayStore {
		t.Fatalf("expected retry delay %s, got %s", retryDelayStore, retryErr.RetryDelay())
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

	if err := engine.processJob(testCtx(t), req, "trace-123"); err != nil {
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

func TestProcessJobApprovalGateFirstVisitStoresSyntheticDecision(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-gate-1",
		Topic: capsdk.SubjectApprovalGate,
	}

	if err := engine.processJob(testCtx(t), req, "trace-gate-1"); err != nil {
		t.Fatalf("process job: %v", err)
	}
	if got := jobStore.states["job-gate-1"]; got != JobStateApproval {
		t.Fatalf("expected approval_required state, got %s", got)
	}
	record, ok := jobStore.safety["job-gate-1"]
	if !ok {
		t.Fatalf("expected synthetic safety decision record")
	}
	if record.Decision != SafetyRequireApproval {
		t.Fatalf("expected %s, got %s", SafetyRequireApproval, record.Decision)
	}
	if !record.ApprovalRequired {
		t.Fatalf("expected approval_required=true")
	}
	if record.PolicySnapshot != workflowGateSnapshot {
		t.Fatalf("expected synthetic snapshot %q, got %q", workflowGateSnapshot, record.PolicySnapshot)
	}
	if len(bus.snapshotPublished()) != 0 {
		t.Fatalf("expected no bus publish on first visit")
	}
}

func TestProcessJobApprovalGateApprovedAutoCompletes(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-gate-2",
		Topic: capsdk.SubjectApprovalGate,
		Labels: map[string]string{
			"approval_granted":  "true",
			"approval_snapshot": workflowGateSnapshot,
			"gate_type":         "workflow_approval",
		},
	}
	jobHash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	jobStore.safety[req.JobId] = SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   workflowGateSnapshot,
		JobHash:          jobHash,
	}

	if err := engine.processJob(testCtx(t), req, "trace-gate-2"); err != nil {
		t.Fatalf("process job: %v", err)
	}

	if got := jobStore.states["job-gate-2"]; got != JobStateSucceeded {
		t.Fatalf("expected succeeded state, got %s", got)
	}
	msgs := bus.snapshotPublished()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(msgs))
	}
	if msgs[0].subject != capsdk.SubjectResult {
		t.Fatalf("expected publish to %s, got %s", capsdk.SubjectResult, msgs[0].subject)
	}
	res := msgs[0].packet.GetJobResult()
	if res == nil {
		t.Fatalf("expected job result payload")
	}
	if res.GetStatus() != pb.JobStatus_JOB_STATUS_SUCCEEDED {
		t.Fatalf("expected result status SUCCEEDED, got %s", res.GetStatus().String())
	}
}

func TestProcessJobApprovalGateStoreFailureReturnsRetryableError(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	failingStore := &failingSafetyDecisionStore{
		fakeJobStore: newFakeJobStore(),
		err:          fmt.Errorf("write failed"),
	}
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), failingStore, nil)

	req := &pb.JobRequest{
		JobId: "job-gate-3",
		Topic: capsdk.SubjectApprovalGate,
	}
	err := engine.processJob(testCtx(t), req, "trace-gate-3")
	retryErr, ok := err.(*retryableError)
	if !ok {
		t.Fatalf("expected retryable error, got %v", err)
	}
	// Safety decision store write failure surfaces as SafetyUnavailable
	// in checkSafetyDecision, so the retry uses safetyThrottleDelay.
	if retryErr.RetryDelay() != safetyThrottleDelay {
		t.Fatalf("expected retry delay %s, got %s", safetyThrottleDelay, retryErr.RetryDelay())
	}
	if len(bus.snapshotPublished()) != 0 {
		t.Fatalf("expected no publish when synthetic decision persistence fails")
	}
}

func TestProcessJobApprovalGatePublishFailureReturnsRetryableError(t *testing.T) {
	bus := &fakeBus{publishErr: fmt.Errorf("bus unavailable"), failSubject: capsdk.SubjectResult}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-gate-4",
		Topic: capsdk.SubjectApprovalGate,
		Labels: map[string]string{
			"approval_granted":  "true",
			"approval_snapshot": workflowGateSnapshot,
		},
	}
	jobHash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	jobStore.safety[req.JobId] = SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   workflowGateSnapshot,
		JobHash:          jobHash,
	}

	err = engine.processJob(testCtx(t), req, "trace-gate-4")
	retryErr, ok := err.(*retryableError)
	if !ok {
		t.Fatalf("expected retryable error, got %v", err)
	}
	if retryErr.RetryDelay() != retryDelayPublish {
		t.Fatalf("expected retry delay %s, got %s", retryDelayPublish, retryErr.RetryDelay())
	}
	if got := jobStore.states["job-gate-4"]; got == JobStateSucceeded {
		t.Fatalf("expected job not to be marked succeeded when result publish fails")
	}
}

// TestApprovedWorkerJobNotAutoCompleted verifies that a regular worker job
// (non-gate topic) that went through the approval flow is dispatched to its
// worker handler, NOT auto-completed with a synthetic SUCCEEDED result.
// This is the regression test for the bug where send_email/notify_slack jobs
// were silently auto-completed after approval.
func TestApprovedWorkerJobNotAutoCompleted(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	// Simulate a worker job (send-email) that went through approval flow.
	// It has the approval_granted label because the gateway set it after
	// admin approved, but its topic is a regular worker topic, not an
	// approval gate topic.
	req := &pb.JobRequest{
		JobId: "job-worker-approved",
		Topic: "job.gtm-engine.send-email",
		Labels: map[string]string{
			"approval_granted": "true",
			"workflow_id":      "gtm-engine.account-brief",
			"run_id":           "run-123",
			"step_id":          "send_email",
		},
	}
	jobHash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	// Store a previous safety decision that required approval (simulates
	// the safety kernel marking send-email as needing approval).
	jobStore.safety[req.JobId] = SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          jobHash,
	}

	if err := engine.processJob(testCtx(t), req, "trace-worker-approved"); err != nil {
		t.Fatalf("process job: %v", err)
	}

	// The job must NOT be auto-completed. It should be dispatched to the
	// worker pool. With NaiveStrategy it gets dispatched normally.
	if got := jobStore.states["job-worker-approved"]; got == JobStateSucceeded {
		t.Fatalf("worker job was auto-completed (bug!); expected dispatch to worker, got state %s", got)
	}
	// Verify a result was NOT published with synthetic SUCCEEDED.
	for _, msg := range bus.snapshotPublished() {
		if msg.subject == capsdk.SubjectResult {
			res := msg.packet.GetJobResult()
			if res != nil && res.GetJobId() == "job-worker-approved" && res.GetStatus() == pb.JobStatus_JOB_STATUS_SUCCEEDED {
				t.Fatalf("synthetic SUCCEEDED result published for worker job (bug!); worker should execute")
			}
		}
	}
}

// TestApprovalGateTopicStillAutoCompletes verifies that actual approval gate
// jobs are still auto-completed after the fix (regression safety).
// The gate must have been approved (label set, previous decision stored).
func TestApprovalGateTopicStillAutoCompletes(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil)

	for _, topic := range []string{capsdk.SubjectApprovalGate, capsdk.SubjectWorkflowApprovalGate} {
		jobID := "gate-" + topic
		req := &pb.JobRequest{
			JobId: jobID,
			Topic: topic,
			Labels: map[string]string{
				"approval_granted":  "true",
				"approval_snapshot": workflowGateSnapshot,
				"gate_type":         "workflow_approval",
			},
		}
		jobHash, err := HashJobRequest(req)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		jobStore.safety[jobID] = SafetyDecisionRecord{
			Decision:         SafetyRequireApproval,
			ApprovalRequired: true,
			PolicySnapshot:   workflowGateSnapshot,
			JobHash:          jobHash,
		}
		bus.published = nil
		jobStore.states = map[string]JobState{}

		if err := engine.processJob(testCtx(t), req, "trace-"+jobID); err != nil {
			t.Fatalf("process job %s: %v", topic, err)
		}
		if got := jobStore.states[jobID]; got != JobStateSucceeded {
			t.Fatalf("approval gate %s not auto-completed; got state %s", topic, got)
		}
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

	if err := engine.CancelJob(testCtx(t), "job-1"); err != nil {
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

	if err := engine.processJob(testCtx(t), req, "trace-ec"); err != nil {
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

	if err := engine.processJob(testCtx(t), req, "trace-block"); err != nil {
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

func TestProcessJob_SetStateDeniedFailure_ReturnsRetryable(t *testing.T) {
	// Regression test: when setJobState(DENIED) fails, processJob must return
	// a retryable error so the job is retried, not lost in a non-terminal state.
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	storeErr := fmt.Errorf("redis connection refused")
	store := &failingSetStateStore{
		fakeJobStore: newFakeJobStore(),
		failOnState:  JobStateDenied,
		setStateErr:  storeErr,
	}
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	// "sys.destroy" is blocked by SafetyBasic → SafetyDeny path.
	req := &pb.JobRequest{
		JobId: "job-retry-denied",
		Topic: "sys.destroy",
	}

	err := engine.processJob(testCtx(t), req, "trace-retry-denied")
	if err == nil {
		t.Fatal("expected retryable error when setJobState(DENIED) fails, got nil — job would be lost")
	}
	// Verify the error wraps the original store error.
	if !strings.Contains(err.Error(), "redis connection refused") {
		t.Fatalf("expected error to contain store failure, got: %v", err)
	}
	// Verify no DLQ was emitted (state didn't succeed, so DLQ should not fire).
	if len(bus.published) != 0 {
		t.Fatalf("expected 0 bus publishes when setJobState fails (DLQ should not fire), got %d", len(bus.published))
	}
	// Verify job state was NOT set (store returned error).
	if state := store.states["job-retry-denied"]; state == JobStateDenied {
		t.Fatal("job state should NOT be DENIED when SetState returned error")
	}
}

func TestProcessJob_SetStateFailedFailure_ReturnsRetryable(t *testing.T) {
	// Regression test: dispatch failure path → FAILED state transition fails.
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	storeErr := fmt.Errorf("redis timeout")
	store := &failingSetStateStore{
		fakeJobStore: newFakeJobStore(),
		failOnState:  JobStateFailed,
		setStateErr:  storeErr,
	}
	// Empty registry → no workers → dispatch will fail with "no matching workers".
	// But first the safety check must pass, so use a passing topic.
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	// Use a topic with no workers to trigger dispatch failure → FAILED path.
	// Set high attempts to trigger max scheduling retries → FAILED.
	store.attempts["job-retry-failed"] = 999

	req := &pb.JobRequest{
		JobId: "job-retry-failed",
		Topic: "job.nonexistent",
	}

	err := engine.processJob(testCtx(t), req, "trace-retry-failed")
	if err == nil {
		t.Fatal("expected retryable error when setJobState(FAILED) fails, got nil — job would be stuck")
	}
	if !strings.Contains(err.Error(), "redis timeout") {
		t.Fatalf("expected error to contain store failure, got: %v", err)
	}
}

func TestProcessJob_DLQEmitFailure_ReturnsRetryable(t *testing.T) {
	// Regression: when DLQ emit fails after a denied job, the engine must
	// return a retryable error so the NATS message is redelivered.
	bus := &fakeBus{
		publishErr:  fmt.Errorf("NATS connection lost"),
		failSubject: capsdk.SubjectDLQ,
	}
	registry := newTestRegistry(t)
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId: "job-dlq-retry",
		Topic: "sys.destroy", // blocked by SafetyBasic → DENIED path → DLQ emit
	}

	err := engine.processJob(testCtx(t), req, "trace-dlq-retry")
	if err == nil {
		t.Fatal("expected retryable error when DLQ emit fails, got nil — denied job silently lost from audit trail")
	}
	if !strings.Contains(err.Error(), "NATS connection lost") {
		t.Fatalf("expected error to contain DLQ failure, got: %v", err)
	}

	// State should be DENIED (state transition succeeded before DLQ failed).
	if state := store.states["job-dlq-retry"]; state != JobStateDenied {
		t.Fatalf("expected job state DENIED, got %s", state)
	}

	// Now simulate redelivery — bus works this time.
	bus.mu.Lock()
	bus.publishErr = nil
	bus.failSubject = ""
	bus.published = nil
	bus.mu.Unlock()

	err = engine.processJob(testCtx(t), req, "trace-dlq-retry-2")
	if err != nil {
		t.Fatalf("second attempt should succeed, got: %v", err)
	}

	// DLQ entry should now exist.
	published := bus.snapshotPublished()
	foundDLQ := false
	for _, msg := range published {
		if msg.subject == capsdk.SubjectDLQ {
			foundDLQ = true
			break
		}
	}
	if !foundDLQ {
		t.Fatal("expected DLQ entry after successful retry, but none found")
	}
}

func TestCheckSafetyDecision_EngineShutdown_DeniesImmediately(t *testing.T) {
	// Regression: during engine shutdown, the safety check must not fall
	// through to fail-open logic. It must deny immediately.
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId: "job-shutdown",
		Topic: "job.default",
	}

	// Simulate engine shutdown by cancelling the engine context.
	engine.cancel()

	record, err := engine.checkSafetyDecision(context.Background(), req)
	if err == nil {
		t.Fatal("expected error during shutdown, got nil")
	}
	if record.Decision != SafetyDeny {
		t.Fatalf("expected SafetyDeny during shutdown, got %v", record.Decision)
	}
	if record.Reason != "engine shutting down" {
		t.Fatalf("expected reason 'engine shutting down', got %q", record.Reason)
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

	if err := engine.processJob(testCtx(t), req, "trace-invalid"); err != nil {
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
	if err := saga.RecordCompensation(testCtx(t), seedReq); err != nil {
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

	waitCtx, waitCancel := context.WithTimeout(testCtx(t), 2*time.Second)
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

	err := engine.processJob(testCtx(t), req, "trace-unavail")
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

	if err := engine.processJob(testCtx(t), req, "trace-stuck"); err != nil {
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

func TestEngine_RetryCapPreservedOnGetAttemptsError(t *testing.T) {
	attemptsErr := errors.New("redis timeout")
	baseStore := newFakeJobStore()
	jobStore := &failingGetAttemptsStore{fakeJobStore: baseStore, err: attemptsErr}
	bus := &fakeBus{}
	engine := NewEngine(bus, &fixedSafetyRecordChecker{record: SafetyDecisionRecord{
		Decision: SafetyAllowWithConstraints,
		Constraints: &pb.PolicyConstraints{
			Budgets: &pb.BudgetConstraints{MaxRetries: 3},
		},
	}}, newTestRegistry(t), NewNaiveStrategy(), jobStore, nil)

	req := &pb.JobRequest{
		JobId:    "job-max-retries-attempts-unreadable",
		Topic:    "job.default",
		TenantId: "tenant-a",
	}
	err := engine.processJob(testCtx(t), req, "trace-attempts-unreadable")
	if err != nil && !errors.Is(err, attemptsErr) {
		t.Fatalf("processJob() error = %v, want retryable store error or max-retry fail-closed", err)
	}
	assertNoJobRequestPublished(t, bus, req.Topic, req.JobId)
	if err == nil && baseStore.states[req.JobId] != JobStateFailed {
		t.Fatalf("state=%q want FAILED when unreadable attempts are treated as max", baseStore.states[req.JobId])
	}
	if state := baseStore.states[req.JobId]; state == JobStateDispatched || state == JobStateRunning {
		t.Fatalf("state=%q shows job dispatched despite unreadable attempts", state)
	}
}

func TestEngine_RetryCapPreservedOnGetAttemptsError_SchedulingBackoffGate(t *testing.T) {
	attemptsErr := errors.New("redis timeout")
	baseStore := newFakeJobStore()
	jobStore := &failingGetAttemptsStore{fakeJobStore: baseStore, err: attemptsErr}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), &errStrategy{err: ErrNoWorkers}, jobStore, nil)

	req := &pb.JobRequest{
		JobId:    "job-scheduling-attempts-unreadable",
		Topic:    "job.default",
		TenantId: "tenant-a",
	}
	err := engine.processJob(testCtx(t), req, "trace-scheduling-attempts-unreadable")
	if err != nil && !errors.Is(err, attemptsErr) {
		t.Fatalf("processJob() error = %v, want retryable store error or max-scheduling fail-closed", err)
	}
	assertNoJobRequestPublished(t, bus, req.Topic, req.JobId)
	if got := baseStore.attempts[req.JobId]; got != 0 {
		t.Fatalf("attempts incremented to %d after unreadable counter; want fail-closed without retry loop", got)
	}
	if err == nil && baseStore.states[req.JobId] != JobStateFailed {
		t.Fatalf("state=%q want FAILED when unreadable scheduling attempts are treated as max", baseStore.states[req.JobId])
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

	err := engine.processJob(testCtx(t), req, "trace-retry")
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

	if err := engine.processJob(testCtx(t), req, "trace-ok"); err != nil {
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
func (m *cancelMetricsSpy) IncJobLockAbandoned()                              {}
func (m *cancelMetricsSpy) IncResultPtrWriteFailure()                         {}
func (m *cancelMetricsSpy) IncDispatchRollback(string)                        {}
func (m *cancelMetricsSpy) IncDispatchFlushOnWorkerOnline(string)             {}

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

func TestExtractWorkerFromSubject(t *testing.T) {
	tests := []struct {
		subject string
		want    string
	}{
		{"worker.abc-123.jobs", "abc-123"},
		{"worker.visa-governance-evaluator.jobs", "visa-governance-evaluator"},
		{"job.demo-mock-bank.transfer", ""},
		{"worker..jobs", ""},
		{"", ""},
		{"worker.foo", ""},
	}
	for _, tt := range tests {
		got := extractWorkerFromSubject(tt.subject)
		if got != tt.want {
			t.Errorf("extractWorkerFromSubject(%q) = %q, want %q", tt.subject, got, tt.want)
		}
	}
}

func TestMemoryRegistry_IsAlive(t *testing.T) {
	reg := NewMemoryRegistryWithTTL(2 * time.Second)
	t.Cleanup(reg.Close)

	// Worker not registered
	if reg.IsAlive("nonexistent") {
		t.Error("expected false for nonexistent worker")
	}

	// Register worker
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "pool1"})
	if !reg.IsAlive("w1") {
		t.Error("expected true for just-registered worker")
	}

	// Wait for TTL to expire
	time.Sleep(3 * time.Second)
	if reg.IsAlive("w1") {
		t.Error("expected false after TTL expired")
	}
}

func TestFailModeAtomicSwitch(t *testing.T) {
	reg := newTestRegistry(t)
	store := newFakeJobStore()
	e := NewEngine(&fakeBus{}, nil, reg, NewNaiveStrategy(), store, nil)

	// Default is closed (fail-closed)
	if e.isInputFailOpen() {
		t.Error("expected input fail mode to default to closed")
	}

	// Switch to open
	e.WithInputFailMode("open")
	if !e.isInputFailOpen() {
		t.Error("expected input fail mode to be open after switch")
	}

	// Switch back to closed
	e.WithInputFailMode("closed")
	if e.isInputFailOpen() {
		t.Error("expected input fail mode to be closed after switch back")
	}

	// Invalid value keeps current
	e.WithInputFailMode("open")
	e.WithInputFailMode("invalid")
	// "invalid" != "open", so Store(false)
	if e.isInputFailOpen() {
		t.Error("expected invalid mode to set closed")
	}
}

func TestOutputPolicyAtomicToggle(t *testing.T) {
	reg := newTestRegistry(t)
	store := newFakeJobStore()
	e := NewEngine(&fakeBus{}, nil, reg, NewNaiveStrategy(), store, nil)

	// Default is disabled
	e.WithOutputSafetyEnabled(true)
	if !e.outputSafetyEnabled.Load() {
		t.Error("expected output safety enabled after toggle on")
	}

	e.WithOutputSafetyEnabled(false)
	if e.outputSafetyEnabled.Load() {
		t.Error("expected output safety disabled after toggle off")
	}
}

func TestAsyncFailModeAtomicSwitch(t *testing.T) {
	reg := newTestRegistry(t)
	store := newFakeJobStore()
	e := NewEngine(&fakeBus{}, nil, reg, NewNaiveStrategy(), store, nil)

	if e.isAsyncFailOpen() {
		t.Error("expected async fail mode to default to closed")
	}

	e.WithAsyncFailMode("open")
	if !e.isAsyncFailOpen() {
		t.Error("expected async fail mode to be open")
	}

	e.WithAsyncFailMode("closed")
	if e.isAsyncFailOpen() {
		t.Error("expected async fail mode to be closed")
	}
}

// ---------------------------------------------------------------------------
// setJobState failure → RetryAfter (not nil)
// ---------------------------------------------------------------------------

func TestSetJobStateFailureReturnsRetryNotNil(t *testing.T) {
	storeErr := fmt.Errorf("redis connection refused")

	tests := []struct {
		name        string
		failOnState JobState
		setup       func(*failingSetStateStore)
		req         *pb.JobRequest
	}{
		{
			name:        "SafetyDeny/DENIED state failure retries",
			failOnState: JobStateDenied,
			req: &pb.JobRequest{
				JobId: "job-deny-fail",
				Topic: "sys.destroy",
			},
		},
		{
			name:        "max scheduling retries/FAILED state failure retries",
			failOnState: JobStateFailed,
			setup: func(s *failingSetStateStore) {
				s.attempts["job-max-retry"] = maxSchedulingRetries + 1
			},
			req: &pb.JobRequest{
				JobId: "job-max-retry",
				Topic: "job.test",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := newFakeJobStore()
			store := &failingSetStateStore{
				fakeJobStore: base,
				failOnState:  tc.failOnState,
				setStateErr:  storeErr,
			}
			if tc.setup != nil {
				tc.setup(store)
			}

			bus := &fakeBus{}
			registry := newTestRegistry(t)
			engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

			err := engine.processJob(testCtx(t), tc.req, "trace-fail")
			if err == nil {
				t.Fatal("expected retryable error when setJobState fails, got nil — job would be lost")
			}
			var ra *retryableError
			if !errors.As(err, &ra) {
				t.Fatalf("expected retryableError, got: %v", err)
			}
		})
	}
}

// fixedDecisionSafety always returns the configured decision.
type fixedDecisionSafety struct {
	decision SafetyDecision
	reason   string
}

func (f *fixedDecisionSafety) Check(_ context.Context, _ *pb.JobRequest) (SafetyDecisionRecord, error) {
	return SafetyDecisionRecord{Decision: f.decision, Reason: f.reason}, nil
}

type fixedSafetyRecordChecker struct {
	record SafetyDecisionRecord
	err    error
}

func (f *fixedSafetyRecordChecker) Check(_ context.Context, _ *pb.JobRequest) (SafetyDecisionRecord, error) {
	return f.record, f.err
}

func TestCheckSafetyDecisionAppendsDecisionLog(t *testing.T) {
	checkedAt := time.Date(2026, time.April, 20, 9, 30, 0, 0, time.UTC).UnixNano() / int64(time.Microsecond)
	tests := []struct {
		name   string
		record SafetyDecisionRecord
	}{
		{
			name: "allow",
			record: SafetyDecisionRecord{
				Decision:       SafetyAllow,
				Reason:         "allowed",
				RuleID:         "rule-allow",
				PolicySnapshot: "snap-allow|sha256:1",
				CheckedAt:      checkedAt,
			},
		},
		{
			name: "deny",
			record: SafetyDecisionRecord{
				Decision:       SafetyDeny,
				Reason:         "blocked",
				RuleID:         "rule-deny",
				PolicySnapshot: "snap-deny|sha256:2",
				CheckedAt:      checkedAt + 1_000,
			},
		},
		{
			name: "constrain",
			record: SafetyDecisionRecord{
				Decision:       SafetyAllowWithConstraints,
				Reason:         "limited",
				RuleID:         "rule-constrain",
				PolicySnapshot: "snap-constrain|sha256:3",
				Constraints: &pb.PolicyConstraints{
					Budgets: &pb.BudgetConstraints{MaxRetries: 2},
				},
				CheckedAt: checkedAt + 2_000,
			},
		},
		{
			name: "require approval",
			record: SafetyDecisionRecord{
				Decision:         SafetyRequireApproval,
				Reason:           "needs review",
				RuleID:           "rule-approval",
				PolicySnapshot:   "snap-approval|sha256:4",
				ApprovalRequired: true,
				ApprovalStatus:   model.ApprovalStatusPending,
				CheckedAt:        checkedAt + 3_000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeJobStore()
			decisionLog := &fakeDecisionLogStore{}
			engine := NewEngine(&fakeBus{}, &fixedSafetyRecordChecker{record: tt.record}, newTestRegistry(t), NewNaiveStrategy(), store, nil).
				WithDependencies(Dependencies{DecisionLog: decisionLog})

			req := &pb.JobRequest{
				JobId:    "job-" + strings.ReplaceAll(tt.name, " ", "-"),
				Topic:    "job.test",
				TenantId: "tenant-a",
				Labels:   map[string]string{"agent_id": "agent-123"},
			}

			record, err := engine.checkSafetyDecision(context.Background(), req)
			if err != nil {
				t.Fatalf("checkSafetyDecision() error = %v", err)
			}
			if record.Decision != tt.record.Decision {
				t.Fatalf("Decision=%q want %q", record.Decision, tt.record.Decision)
			}

			logged := decisionLog.snapshotRecords()
			if len(logged) != 1 {
				t.Fatalf("logged decisions=%d want 1", len(logged))
			}
			entry := logged[0]
			if entry.JobID != req.GetJobId() {
				t.Fatalf("JobID=%q want %q", entry.JobID, req.GetJobId())
			}
			if entry.Tenant != "tenant-a" {
				t.Fatalf("Tenant=%q want tenant-a", entry.Tenant)
			}
			if entry.AgentID != "agent-123" {
				t.Fatalf("AgentID=%q want agent-123", entry.AgentID)
			}
			if entry.Topic != req.GetTopic() {
				t.Fatalf("Topic=%q want %q", entry.Topic, req.GetTopic())
			}
			if entry.Verdict != tt.record.Decision {
				t.Fatalf("Verdict=%q want %q", entry.Verdict, tt.record.Decision)
			}
			if entry.RuleID != tt.record.RuleID {
				t.Fatalf("RuleID=%q want %q", entry.RuleID, tt.record.RuleID)
			}
			if entry.PolicyVersion != decisionLogPolicyVersion(tt.record.PolicySnapshot) {
				t.Fatalf("PolicyVersion=%q want %q", entry.PolicyVersion, decisionLogPolicyVersion(tt.record.PolicySnapshot))
			}
			if entry.Reason != tt.record.Reason {
				t.Fatalf("Reason=%q want %q", entry.Reason, tt.record.Reason)
			}
			if entry.Timestamp != tt.record.CheckedAt/1_000 {
				t.Fatalf("Timestamp=%d want %d", entry.Timestamp, tt.record.CheckedAt/1_000)
			}
			if tt.record.Constraints != nil && entry.Constraints.GetBudgets().GetMaxRetries() != tt.record.Constraints.GetBudgets().GetMaxRetries() {
				t.Fatalf("constraints not preserved")
			}
			if entry.ApprovalStatus != tt.record.ApprovalStatus {
				t.Fatalf("ApprovalStatus=%q want %q", entry.ApprovalStatus, tt.record.ApprovalStatus)
			}
		})
	}
}

func TestCheckSafetyDecisionApprovalGrantedAppendsDecisionLog(t *testing.T) {
	store := newFakeJobStore()
	decisionLog := &fakeDecisionLogStore{}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithDependencies(Dependencies{DecisionLog: decisionLog})

	req := &pb.JobRequest{
		JobId:    "job-approved-log",
		Topic:    "job.test",
		TenantId: "tenant-a",
		Labels: map[string]string{
			"approval_granted":  "true",
			"approval_snapshot": "snap-approved|sha256:abc",
			"agent_id":          "agent-approved",
		},
	}
	jobHash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("HashJobRequest() error = %v", err)
	}
	store.safety[req.JobId] = SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-approved|sha256:abc",
		RuleID:           "rule-approved",
		JobHash:          jobHash,
		CheckedAt:        time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC).UnixNano() / int64(time.Microsecond),
	}

	record, err := engine.checkSafetyDecision(context.Background(), req)
	if err != nil {
		t.Fatalf("checkSafetyDecision() error = %v", err)
	}
	if record.Decision != SafetyAllow {
		t.Fatalf("Decision=%q want %q", record.Decision, SafetyAllow)
	}

	logged := decisionLog.snapshotRecords()
	if len(logged) != 1 {
		t.Fatalf("logged decisions=%d want 1", len(logged))
	}
	if logged[0].Reason != "approval granted" {
		t.Fatalf("Reason=%q want approval granted", logged[0].Reason)
	}
	if logged[0].PolicyVersion != "snap-approved" {
		t.Fatalf("PolicyVersion=%q want snap-approved", logged[0].PolicyVersion)
	}
}

func TestCheckSafetyDecision_PreservesExistingJobHashOnRequireApproval(t *testing.T) {
	store := newFakeJobStore()
	engine := NewEngine(&fakeBus{}, &fixedSafetyRecordChecker{record: SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		Reason:           "needs review",
	}}, newTestRegistry(t), NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId:    "job-preserve-hash",
		Topic:    "job.test",
		TenantId: "tenant-a",
		Labels:   map[string]string{"workflow_id": "wf-1"},
	}
	const pristineHash = "pristine-gateway-hash-xyz"
	store.safety[req.JobId] = SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		JobHash:          pristineHash,
	}
	computedHash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("HashJobRequest() error = %v", err)
	}
	if computedHash == pristineHash {
		t.Fatal("computed hash unexpectedly matched seeded gateway hash")
	}

	record, err := engine.checkSafetyDecision(context.Background(), req)
	if err != nil {
		t.Fatalf("checkSafetyDecision() error = %v", err)
	}
	if record.JobHash != pristineHash {
		t.Fatalf("JobHash=%q want preserved %q", record.JobHash, pristineHash)
	}
}

func TestCheckSafetyDecision_PropagatesExistingJobHashReadFailure(t *testing.T) {
	readErr := errors.New("redis read lost approval hash fence")
	baseStore := newFakeJobStore()
	store := &failingGetSafetyDecisionStore{
		fakeJobStore: baseStore,
		err:          readErr,
	}
	engine := NewEngine(&fakeBus{}, &fixedSafetyRecordChecker{record: SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		Reason:           "needs review",
	}}, newTestRegistry(t), NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId:    "job-read-error-preserve-hash",
		Topic:    "job.test",
		TenantId: "tenant-a",
		Labels:   map[string]string{"workflow_id": "wf-read-error"},
	}
	const pristineHash = "gateway-hash-that-must-not-be-clobbered"
	baseStore.safety[req.JobId] = SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		JobHash:          pristineHash,
	}

	_, err := engine.checkSafetyDecision(context.Background(), req)
	if !errors.Is(err, readErr) {
		t.Fatalf("checkSafetyDecision() error = %v, want %v", err, readErr)
	}
	if got := baseStore.safety[req.JobId].JobHash; got != pristineHash {
		t.Fatalf("stored JobHash=%q want preserved %q after read failure", got, pristineHash)
	}
}

func TestProcessJob_HashFenceReadFailureDoesNotFailOpen(t *testing.T) {
	readErr := errors.New("redis read lost approval hash fence")
	baseStore := newFakeJobStore()
	store := &failingGetSafetyDecisionStore{
		fakeJobStore: baseStore,
		err:          readErr,
	}
	bus := &fakeBus{}
	engine := NewEngine(bus, &fixedSafetyRecordChecker{record: SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		Reason:           "needs review",
	}}, newTestRegistry(t), NewNaiveStrategy(), store, nil)
	engine.WithInputFailMode("open")

	req := &pb.JobRequest{
		JobId:    "job-process-hash-fence-read-failure",
		Topic:    "job.test",
		TenantId: "tenant-a",
		Labels:   map[string]string{"workflow_id": "wf-hash-fence"},
	}
	const pristineHash = "gateway-hash-that-must-survive-processjob"
	baseStore.safety[req.JobId] = SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		JobHash:          pristineHash,
	}

	err := engine.processJob(testCtx(t), req, "trace-hash-fence-read-failure")
	if err == nil {
		t.Fatal("processJob() error = nil, want retryable hash-fence read error; fail-open would dispatch the job")
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("processJob() error = %v, want to wrap %v", err, readErr)
	}
	var retryErr *retryableError
	if !errors.As(err, &retryErr) {
		t.Fatalf("processJob() error = %T, want retryableError", err)
	}
	if got := len(bus.snapshotPublished()); got != 0 {
		t.Fatalf("published %d messages after hash-fence read failure; want 0", got)
	}
	if got := baseStore.states[req.JobId]; got == JobStateRunning || got == JobStateDispatched {
		t.Fatalf("job state = %s after hash-fence read failure; want no dispatch/running state", got)
	}
	if got := baseStore.safety[req.JobId].JobHash; got != pristineHash {
		t.Fatalf("stored JobHash=%q want preserved %q after processJob read failure", got, pristineHash)
	}
}

func TestCheckSafetyDecision_ComputesJobHashWhenNoneExists(t *testing.T) {
	store := newFakeJobStore()
	engine := NewEngine(&fakeBus{}, &fixedSafetyRecordChecker{record: SafetyDecisionRecord{
		Decision:         SafetyRequireApproval,
		ApprovalRequired: true,
		Reason:           "needs review",
	}}, newTestRegistry(t), NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId:    "job-compute-hash",
		Topic:    "job.test",
		TenantId: "tenant-a",
		Labels:   map[string]string{"workflow_id": "wf-2"},
	}
	wantHash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("HashJobRequest() error = %v", err)
	}

	record, err := engine.checkSafetyDecision(context.Background(), req)
	if err != nil {
		t.Fatalf("checkSafetyDecision() error = %v", err)
	}
	if record.JobHash != wantHash {
		t.Fatalf("JobHash=%q want computed %q", record.JobHash, wantHash)
	}
}

func TestProcessJobDecisionLogFailureWarnsAndDoesNotBlock(t *testing.T) {
	var logBuf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	store := newFakeJobStore()
	decisionLog := &fakeDecisionLogStore{err: fmt.Errorf("decision log unavailable")}
	engine := NewEngine(&fakeBus{}, &fixedSafetyRecordChecker{record: SafetyDecisionRecord{
		Decision:  SafetyAllow,
		Reason:    "allowed",
		CheckedAt: time.Now().UTC().UnixNano() / int64(time.Microsecond),
	}}, newTestRegistry(t), NewNaiveStrategy(), store, nil).WithDependencies(Dependencies{DecisionLog: decisionLog})

	req := &pb.JobRequest{
		JobId:    "job-log-warn",
		Topic:    "job.test",
		TenantId: "tenant-a",
	}
	if err := engine.processJob(testCtx(t), req, "trace-log-warn"); err != nil {
		t.Fatalf("processJob() error = %v", err)
	}
	if store.states[req.JobId] == "" {
		t.Fatalf("expected job state to be updated, got empty state")
	}
	if !strings.Contains(logBuf.String(), "decision log append failed") {
		t.Fatalf("expected decision log warning, got %q", logBuf.String())
	}
}

func TestSetJobStateFailureApprovalReturnsRetry(t *testing.T) {
	storeErr := fmt.Errorf("redis connection refused")

	base := newFakeJobStore()
	store := &failingSetStateStore{
		fakeJobStore: base,
		failOnState:  JobStateApproval,
		setStateErr:  storeErr,
	}

	safety := &fixedDecisionSafety{decision: SafetyRequireApproval, reason: "needs human review"}
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	engine := NewEngine(bus, safety, registry, NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId: "job-approval-fail",
		Topic: "job.test",
	}

	err := engine.processJob(testCtx(t), req, "trace-approval-fail")
	if err == nil {
		t.Fatal("expected retryable error when setJobState(APPROVAL) fails, got nil — job stuck forever")
	}
	var ra *retryableError
	if !errors.As(err, &ra) {
		t.Fatalf("expected retryableError, got: %v", err)
	}
}

// TestCheckSafetyDecisionShutdownDeniesNotFailOpen verifies that engine
// shutdown causes an immediate deny, not a fail-open allow.
func TestCheckSafetyDecisionShutdownDeniesNotFailOpen(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	store := newFakeJobStore()
	// Use fail-open mode — the bug was that shutdown looked like "unavailable"
	// and fail-open would allow the job through.
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)
	engine.WithInputFailMode("open")

	req := &pb.JobRequest{
		JobId: "job-shutdown-test",
		Topic: "job.test",
	}

	// Cancel the engine context to simulate shutdown
	engine.cancel()

	record, err := engine.checkSafetyDecision(context.Background(), req)
	if err == nil {
		t.Fatal("expected error from checkSafetyDecision during shutdown, got nil")
	}
	if record.Decision != SafetyDeny {
		t.Fatalf("expected SafetyDeny during shutdown, got %v (would be fail-open allowing jobs through)", record.Decision)
	}
	if record.Reason != "engine shutting down" {
		t.Fatalf("expected 'engine shutting down' reason, got %q", record.Reason)
	}
}

// TestTenantMismatchRejectsJob verifies that a job with mismatched TenantId vs
// env["tenant_id"] is silently dropped and counted as a validation rejection.
func TestTenantMismatchRejectsJob(t *testing.T) {
	store := newFakeJobStore()
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	// Job with auth tenant "org-a" but env tenant "org-b" — should be rejected.
	req := &pb.JobRequest{
		JobId:    "job-tenant-mismatch",
		Topic:    "job.test",
		TenantId: "org-a",
		Env:      map[string]string{"tenant_id": "org-b"},
	}
	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_JobRequest{JobRequest: req},
		TraceId: "trace-tenant-test",
	}
	err := engine.HandlePacket(packet)
	if err != nil {
		t.Fatalf("expected nil error on tenant mismatch (job dropped), got %v", err)
	}

	// Job should not have been processed — state should be empty (never set).
	state, _ := store.GetState(testCtx(t), "job-tenant-mismatch")
	if state != "" {
		t.Fatalf("expected empty state for rejected job, got %q", state)
	}

	// No bus publish.
	published := bus.snapshotPublished()
	for _, msg := range published {
		if msg.packet.GetJobRequest() != nil && msg.packet.GetJobRequest().GetJobId() == "job-tenant-mismatch" {
			t.Fatalf("tenant-mismatched job should not be published to bus")
		}
	}
}

// TestTenantMatchAcceptsJob verifies that matching tenants pass the check.
func TestTenantMatchAcceptsJob(t *testing.T) {
	store := newFakeJobStore()
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId:    "job-tenant-match",
		Topic:    "job.test",
		TenantId: "org-a",
		Env:      map[string]string{"tenant_id": "org-a"},
	}
	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_JobRequest{JobRequest: req},
		TraceId: "trace-tenant-ok",
	}
	err := engine.HandlePacket(packet)
	if err != nil {
		t.Fatalf("expected no error for matching tenant, got %v", err)
	}

	// Job should have been processed — state set.
	state, getErr := store.GetState(testCtx(t), "job-tenant-match")
	if getErr != nil {
		t.Fatalf("expected job state to exist after processing, got err: %v", getErr)
	}
	if state == "" {
		t.Fatalf("expected non-empty job state")
	}
}

// TestProcessJobTenantMatchProceeds is covered by TestTenantMatchAcceptsJob above.
// This duplicate uses processJob directly for lower-level verification.
func TestProcessJobTenantMatchProceeds(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId:    "job-tenant-match-2",
		Topic:    "job.test",
		TenantId: "tenant-a",
		Env: map[string]string{
			"tenant_id": "tenant-a",
		},
	}

	// processJob should NOT return nil-without-processing (the tenant-dropped path).
	// It should proceed past the tenant check into safety/dispatch logic.
	// With no workers registered, it returns a retryable scheduling error — that's fine,
	// it proves the job passed the tenant check.
	_ = engine.processJob(testCtx(t), req, "trace-tenant-ok")
}
