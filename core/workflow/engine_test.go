package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type pubMsg struct {
	subject string
	packet  *pb.BusPacket
}

type recordingBus struct {
	mu        sync.Mutex
	published []pubMsg
}

func (b *recordingBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, pubMsg{subject: subject, packet: packet})
	return nil
}

func (b *recordingBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	return nil
}

func (b *recordingBus) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

func (b *recordingBus) Snapshot() []pubMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]pubMsg, len(b.published))
	copy(out, b.published)
	return out
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

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-foreach",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"fan": {
				ID:      "fan",
				Type:    StepTypeWorker,
				Topic:   "job.default",
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
	if bus.Count() != 2 {
		t.Fatalf("expected 2 fan-out publishes, got %d", bus.Count())
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-foreach:fan[0]@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-foreach:fan[1]@1",
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

	bus := &recordingBus{}
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
	if bus.Count() != 1 {
		t.Fatalf("expected 1 publish, got %d", bus.Count())
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-retry:step@1",
		Status: pb.JobStatus_JOB_STATUS_FAILED,
	})

	// Wait for backoff retry to trigger.
	time.Sleep(1200 * time.Millisecond)
	if bus.Count() < 2 {
		t.Fatalf("expected retry publish, got %d", bus.Count())
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-retry:step@2",
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

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-approval",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"approve": {ID: "approve", Type: StepTypeApproval},
			"work":    {ID: "work", Type: StepTypeWorker, Topic: "job.default", DependsOn: []string{"approve"}},
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
	if bus.Count() != 0 {
		t.Fatalf("expected no publishes before approval, got %d", bus.Count())
	}
	stored, _ := store.GetRun(context.Background(), run.ID)
	if stored.Status != RunStatusWaiting {
		t.Fatalf("expected run waiting, got %s", stored.Status)
	}

	if err := engine.ApproveStep(context.Background(), run.ID, "approve", true); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if bus.Count() != 1 {
		t.Fatalf("expected downstream publish after approval, got %d", bus.Count())
	}
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  runID + ":work@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
}

func TestEngineStepMetadataPropagates(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-meta",
		OrgID: "tenant-1",
		Steps: map[string]*Step{
			"work": {
				ID:    "work",
				Type:  StepTypeWorker,
				Topic: "job.default",
				Meta: &StepMeta{
					ActorId:        "actor-1",
					ActorType:      "human",
					IdempotencyKey: "idem-1",
					PackId:         "pack-1",
					Capability:     "repo.scan",
					RiskTags:       []string{"prod", " network "},
					Requires:       []string{"git", " "},
					Labels:         map[string]string{"team": "blue"},
				},
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-meta",
		WorkflowID: wf.ID,
		OrgID:      "tenant-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 1 {
		t.Fatalf("expected 1 publish, got %d", bus.Count())
	}
	msgs := bus.Snapshot()
	req := msgs[0].packet.GetJobRequest()
	if req == nil {
		t.Fatalf("expected job request")
	}
	if req.PrincipalId != "actor-1" {
		t.Fatalf("expected principal_id actor-1, got %q", req.PrincipalId)
	}
	if req.Meta == nil {
		t.Fatalf("expected job metadata")
	}
	if req.Meta.TenantId != "tenant-1" {
		t.Fatalf("expected tenant_id tenant-1, got %q", req.Meta.TenantId)
	}
	if req.Meta.ActorId != "actor-1" {
		t.Fatalf("expected actor_id actor-1, got %q", req.Meta.ActorId)
	}
	if req.Meta.ActorType != pb.ActorType_ACTOR_TYPE_HUMAN {
		t.Fatalf("expected actor_type human, got %v", req.Meta.ActorType)
	}
	if req.Meta.IdempotencyKey != "idem-1" {
		t.Fatalf("expected idempotency_key idem-1, got %q", req.Meta.IdempotencyKey)
	}
	if req.Meta.PackId != "pack-1" {
		t.Fatalf("expected pack_id pack-1, got %q", req.Meta.PackId)
	}
	if req.Meta.Capability != "repo.scan" {
		t.Fatalf("expected capability repo.scan, got %q", req.Meta.Capability)
	}
	if len(req.Meta.RiskTags) != 2 || req.Meta.RiskTags[1] != "network" {
		t.Fatalf("expected risk_tags trimmed, got %v", req.Meta.RiskTags)
	}
	if len(req.Meta.Requires) != 1 || req.Meta.Requires[0] != "git" {
		t.Fatalf("expected requires trimmed, got %v", req.Meta.Requires)
	}
	if req.Meta.Labels["team"] != "blue" {
		t.Fatalf("expected labels team=blue, got %v", req.Meta.Labels)
	}
}

func TestEngineDelayStepCompletes(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-delay",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"wait": {ID: "wait", Type: StepTypeDelay, DelaySec: 1},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-delay",
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
	if bus.Count() != 0 {
		t.Fatalf("expected no publishes for delay step, got %d", bus.Count())
	}

	time.Sleep(1200 * time.Millisecond)

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded after delay, got %s", final.Status)
	}
	if final.Steps["wait"].Status != StepStatusSucceeded {
		t.Fatalf("expected delay step succeeded, got %s", final.Steps["wait"].Status)
	}
}

func TestEngineNotifyStepEmitsEvent(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-notify",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"notify": {ID: "notify", Type: StepTypeNotify, Input: map[string]any{"message": "hello"}},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-notify",
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
	if bus.Count() != 1 {
		t.Fatalf("expected 1 publish for notify step, got %d", bus.Count())
	}
	msgs := bus.Snapshot()
	if msgs[0].subject != capsdk.SubjectWorkflowEvent {
		t.Fatalf("expected subject %s, got %s", capsdk.SubjectWorkflowEvent, msgs[0].subject)
	}
	if alert := msgs[0].packet.GetAlert(); alert == nil || alert.GetMessage() != "hello" {
		t.Fatalf("expected alert message 'hello'")
	}
	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded after notify, got %s", final.Status)
	}
}

func TestEngineConditionStepEvaluates(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-condition",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"cond": {ID: "cond", Type: StepTypeCondition, Condition: "input.allow", OutputPath: "decision.allowed"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-condition",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{"allow": true},
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	if final.Steps["cond"].Status != StepStatusSucceeded {
		t.Fatalf("expected condition step succeeded, got %s", final.Steps["cond"].Status)
	}
	if final.Context == nil {
		t.Fatalf("expected context to be recorded")
	}
	stepsRaw, ok := final.Context["steps"].(map[string]any)
	if !ok || stepsRaw == nil {
		t.Fatalf("expected steps context to be recorded")
	}
	entry, ok := stepsRaw["cond"].(map[string]any)
	if !ok {
		t.Fatalf("expected condition output entry")
	}
	if entry["output"] != true {
		t.Fatalf("expected condition output true, got %#v", entry["output"])
	}
	decisionRaw, ok := final.Context["decision"].(map[string]any)
	if !ok || decisionRaw["allowed"] != true {
		t.Fatalf("expected output path decision.allowed to be true")
	}
}
