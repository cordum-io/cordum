package scheduler

import (
	"context"
	"testing"

	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type publishedMsg struct {
	subject string
	packet  *pb.BusPacket
}

type fakeBus struct {
	published []publishedMsg
}

type fakeJobStore struct {
	states map[string]JobState
	ptrs   map[string]string
}

func newFakeJobStore() *fakeJobStore {
	return &fakeJobStore{
		states: make(map[string]JobState),
		ptrs:   make(map[string]string),
	}
}

func (s *fakeJobStore) SetState(_ context.Context, jobID string, state JobState) error {
	s.states[jobID] = state
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

func (b *fakeBus) Publish(subject string, packet *pb.BusPacket) error {
	b.published = append(b.published, publishedMsg{subject: subject, packet: packet})
	return nil
}

func (b *fakeBus) Subscribe(subject, queue string, handler func(*pb.BusPacket)) error {
	// Tests call handlers directly, so no-op is fine here.
	return nil
}

func TestEngineHandleHeartbeatStoresWorker(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	engine := NewEngine(bus, NewSafetyStub(), registry, NewNaiveStrategy(), newFakeJobStore(), nil)

	packet := &pb.BusPacket{
		SenderId:        "worker-1",
		TraceId:         "trace-hb",
		ProtocolVersion: 1,
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
	engine := NewEngine(bus, NewSafetyStub(), registry, strategy, jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-1",
		Topic: "job.echo",
	}

	engine.processJob(req, "trace-123")

	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(bus.published))
	}
	if state := jobStore.states["job-1"]; state != JobStateRunning {
		t.Fatalf("expected job state RUNNING, got %s", state)
	}
	msg := bus.published[0]
	if msg.subject != "job.echo" {
		t.Fatalf("expected subject job.echo, got %s", msg.subject)
	}
	if got := msg.packet.GetJobRequest().JobId; got != "job-1" {
		t.Fatalf("expected job_id job-1, got %s", got)
	}
	if msg.packet.TraceId != "trace-123" {
		t.Fatalf("expected trace_id trace-123, got %s", msg.packet.TraceId)
	}
}

func TestProcessJobBlockedBySafety(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyStub(), registry, NewNaiveStrategy(), jobStore, nil)

	req := &pb.JobRequest{
		JobId: "job-blocked",
		Topic: "sys.destroy",
	}

	engine.processJob(req, "trace-block")

	if len(bus.published) != 0 {
		t.Fatalf("expected 0 publishes when safety blocks, got %d", len(bus.published))
	}
	if state := jobStore.states["job-blocked"]; state != JobStateDenied {
		t.Fatalf("expected job state DENIED, got %s", state)
	}
}

func TestProcessJobSkipsInvalidRequest(t *testing.T) {
	bus := &fakeBus{}
	registry := NewMemoryRegistry()
	engine := NewEngine(bus, NewSafetyStub(), registry, NewNaiveStrategy(), newFakeJobStore(), nil)

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
	engine := NewEngine(bus, NewSafetyStub(), registry, NewNaiveStrategy(), jobStore, nil)

	res := &pb.JobResult{
		JobId:     "job-1",
		Status:    pb.JobStatus_JOB_STATUS_COMPLETED,
		ResultPtr: "redis://res:job-1",
		WorkerId:  "worker-1",
	}

	engine.handleJobResult(res)

	if state := jobStore.states["job-1"]; state != JobStateCompleted {
		t.Fatalf("expected job state COMPLETED, got %s", state)
	}
	if ptr := jobStore.ptrs["job-1"]; ptr != "redis://res:job-1" {
		t.Fatalf("expected result ptr redis://res:job-1, got %s", ptr)
	}
}
