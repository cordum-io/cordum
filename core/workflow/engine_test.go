package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

type pubMsg struct {
	subject string
	packet  *pb.BusPacket
}

type stubBus struct {
	mu        sync.Mutex
	published []pubMsg
}

func (b *stubBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, pubMsg{subject: subject, packet: packet})
	return nil
}

func (b *stubBus) Subscribe(subject, queue string, handler func(*pb.BusPacket)) error {
	return nil
}

func newWorkflowStore(t *testing.T) *RedisStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisWorkflowStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("workflow store init: %v", err)
	}
	return store
}

func TestEngineForEachFanoutAndAggregateSuccess(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &stubBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-foreach",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"fan": {
				ID:      "fan",
				Type:    StepTypeWorker,
				Topic:   "job.echo",
				ForEach: "input.items",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-foreach",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      map[string]any{"items": []any{"a", "b"}},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if len(bus.published) != 2 {
		t.Fatalf("expected 2 fan-out publishes, got %d", len(bus.published))
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-foreach:fan[0]",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-foreach:fan[1]",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	if final.Steps["fan"].Status != StepStatusSucceeded {
		t.Fatalf("expected parent step succeeded, got %s", final.Steps["fan"].Status)
	}
}

func TestEngineRetriesAndBackoff(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &stubBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-retry",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step": {
				ID:    "step",
				Type:  StepTypeWorker,
				Topic: "job.retry",
				Retry: &RetryConfig{
					MaxRetries:        1,
					InitialBackoffSec: 1,
					Multiplier:        1,
				},
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-retry",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(bus.published))
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-retry:step",
		Status: pb.JobStatus_JOB_STATUS_FAILED,
	})

	// Wait for backoff retry to trigger.
	time.Sleep(1200 * time.Millisecond)
	if len(bus.published) < 2 {
		t.Fatalf("expected retry publish, got %d", len(bus.published))
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-retry:step",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded after retry, got %s", final.Status)
	}
}

func TestEngineApprovalPausesAndResumes(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &stubBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-approval",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"approve": {ID: "approve", Type: StepTypeApproval},
			"work":    {ID: "work", Type: StepTypeWorker, Topic: "job.echo", DependsOn: []string{"approve"}},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	runID := uuid.NewString()
	run := &WorkflowRun{
		ID:         runID,
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if len(bus.published) != 0 {
		t.Fatalf("expected no publishes before approval, got %d", len(bus.published))
	}
	stored, _ := store.GetRun(context.Background(), run.ID)
	if stored.Status != RunStatusWaiting {
		t.Fatalf("expected run waiting, got %s", stored.Status)
	}

	if err := engine.ApproveStep(context.Background(), run.ID, "approve", true); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(bus.published) != 1 {
		t.Fatalf("expected downstream publish after approval, got %d", len(bus.published))
	}
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  runID + ":work",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
}
