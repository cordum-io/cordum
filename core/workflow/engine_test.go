package workflow

import (
	"context"
	"fmt"
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

func TestEngineForEachFanoutLimitExceeded(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus).WithMaxForEachItems(1)

	wf := &Workflow{
		ID:    "wf-foreach-limit",
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
		ID:         "run-foreach-limit",
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
	if bus.Count() != 0 {
		t.Fatalf("expected no fan-out publishes, got %d", bus.Count())
	}

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	if final.Steps["fan"] == nil || final.Steps["fan"].Status != StepStatusFailed {
		t.Fatalf("expected parent step failed, got %#v", final.Steps["fan"])
	}
	if msg, ok := final.Steps["fan"].Error["message"].(string); !ok || msg == "" {
		t.Fatalf("expected error message on step")
	}

	events, err := store.ListTimelineEvents(context.Background(), run.ID, 20)
	if err != nil {
		t.Fatalf("list timeline: %v", err)
	}
	if !hasTimelineEvent(events, "step_foreach_failed") {
		t.Fatalf("expected step_foreach_failed timeline event")
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

	// Poll until the backoff retry triggers a second publish.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if bus.Count() >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
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

	// Poll until the delay step completes and the run succeeds.
	deadline := time.Now().Add(5 * time.Second)
	var final *WorkflowRun
	for time.Now().Before(deadline) {
		final, _ = store.GetRun(context.Background(), run.ID)
		if final.Status == RunStatusSucceeded {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if final == nil || final.Status != RunStatusSucceeded {
		status := "nil"
		if final != nil {
			status = string(final.Status)
		}
		t.Fatalf("expected run succeeded after delay, got %s", status)
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

func TestEngineConditionEvalErrorFailsRun(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-condition-error",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.default", Condition: "!"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-condition-error",
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
		t.Fatalf("expected no dispatches on condition eval error, got %d", bus.Count())
	}

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	if final.Steps["step"] == nil || final.Steps["step"].Status != StepStatusFailed {
		t.Fatalf("expected step failed, got %#v", final.Steps["step"])
	}
	if msg, ok := final.Steps["step"].Error["message"].(string); !ok || msg == "" {
		t.Fatalf("expected error message on step")
	}

	events, err := store.ListTimelineEvents(context.Background(), run.ID, 20)
	if err != nil {
		t.Fatalf("list timeline: %v", err)
	}
	if !hasTimelineEvent(events, "step_condition_failed") {
		t.Fatalf("expected step_condition_failed timeline event")
	}
}

func TestEngineForEachEvalErrorFailsRun(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-foreach-error",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"fan": {ID: "fan", Type: StepTypeWorker, Topic: "job.default", ForEach: "input.value"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-foreach-error",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{"value": "not-array"},
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
		t.Fatalf("expected no dispatches on for_each eval error, got %d", bus.Count())
	}

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	if final.Steps["fan"] == nil || final.Steps["fan"].Status != StepStatusFailed {
		t.Fatalf("expected step failed, got %#v", final.Steps["fan"])
	}

	events, err := store.ListTimelineEvents(context.Background(), run.ID, 20)
	if err != nil {
		t.Fatalf("list timeline: %v", err)
	}
	if !hasTimelineEvent(events, "step_foreach_failed") {
		t.Fatalf("expected step_foreach_failed timeline event")
	}
}

func hasTimelineEvent(events []TimelineEvent, eventType string) bool {
	for _, evt := range events {
		if evt.Type == eventType {
			return true
		}
	}
	return false
}

// --- scheduleAfter / cancellable timer tests ---

func TestScheduleAfterFiresTimer(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	defer engine.Stop()

	// Create a workflow and run with a delay step so scheduleAfter is exercised.
	wf := &Workflow{
		ID:    "wf-delay-fire",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"wait": {
				ID:       "wait",
				Type:     StepTypeDelay,
				DelaySec: 0, // zero delay completes immediately — not what we want
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-delay-fire",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Directly test scheduleAfter with a short delay.
	engine.scheduleAfter(50*time.Millisecond, wf.ID, run.ID)
	if n := engine.PendingTimers(); n != 1 {
		t.Fatalf("expected 1 pending timer, got %d", n)
	}

	// Wait for it to fire.
	time.Sleep(200 * time.Millisecond)
	if n := engine.PendingTimers(); n != 0 {
		t.Fatalf("expected 0 pending timers after fire, got %d", n)
	}
}

func TestScheduleAfterMultipleTimers(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	defer engine.Stop()

	wf := &Workflow{
		ID:    "wf-multi-timer",
		OrgID: "org-1",
		Steps: map[string]*Step{},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	// Schedule several timers.
	for i := 0; i < 5; i++ {
		runID := uuid.NewString()
		run := &WorkflowRun{
			ID:         runID,
			WorkflowID: wf.ID,
			OrgID:      "org-1",
			Status:     RunStatusPending,
			Steps:      map[string]*StepRun{},
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
		if err := store.CreateRun(context.Background(), run); err != nil {
			t.Fatalf("create run: %v", err)
		}
		engine.scheduleAfter(50*time.Millisecond, wf.ID, runID)
	}

	if n := engine.PendingTimers(); n != 5 {
		t.Fatalf("expected 5 pending timers, got %d", n)
	}

	// After all fire, should drain to 0.
	time.Sleep(300 * time.Millisecond)
	if n := engine.PendingTimers(); n != 0 {
		t.Fatalf("expected 0 pending timers, got %d", n)
	}
}

func TestStopCancelsPendingTimers(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-stop-cancel",
		OrgID: "org-1",
		Steps: map[string]*Step{},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	// Schedule timers with a long delay.
	for i := 0; i < 3; i++ {
		runID := uuid.NewString()
		run := &WorkflowRun{
			ID:         runID,
			WorkflowID: wf.ID,
			OrgID:      "org-1",
			Status:     RunStatusPending,
			Steps:      map[string]*StepRun{},
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
		if err := store.CreateRun(context.Background(), run); err != nil {
			t.Fatalf("create run: %v", err)
		}
		engine.scheduleAfter(10*time.Second, wf.ID, runID)
	}

	if n := engine.PendingTimers(); n != 3 {
		t.Fatalf("expected 3 pending timers, got %d", n)
	}

	// Stop should cancel all immediately.
	engine.Stop()
	if n := engine.PendingTimers(); n != 0 {
		t.Fatalf("expected 0 pending timers after Stop, got %d", n)
	}

	// Calling Stop again should be safe.
	engine.Stop()
}

func TestScheduleAfterIgnoredAfterStop(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Stop first.
	engine.Stop()

	// Now schedule — should be silently dropped.
	engine.scheduleAfter(10*time.Millisecond, "wf-x", "run-x")
	if n := engine.PendingTimers(); n != 0 {
		t.Fatalf("expected no timers after Stop, got %d", n)
	}
}

func TestScheduleAfterZeroDelayIgnored(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	defer engine.Stop()

	engine.scheduleAfter(0, "wf", "run")
	engine.scheduleAfter(-1*time.Second, "wf", "run")
	if n := engine.PendingTimers(); n != 0 {
		t.Fatalf("expected 0 timers for zero/negative delay, got %d", n)
	}
}

// --- on_error handler tests ---

func TestOnError_RedirectsToHandlerOnFailure(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wfDef := &Workflow{
		ID:    "wf-onerr",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"main": {
				ID:      "main",
				Type:    StepTypeWorker,
				Topic:   "job.default",
				OnError: "handler",
			},
			"handler": {
				ID:    "handler",
				Type:  StepTypeWorker,
				Topic: "job.default",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-onerr",
		WorkflowID: wfDef.ID,
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Fail the main step.
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-onerr:main@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "something broke",
	})

	// Run should NOT be failed yet — on_error handler should be dispatched.
	mid, _ := store.GetRun(context.Background(), run.ID)
	if mid.Status == RunStatusFailed {
		t.Fatal("run should not be FAILED while on_error handler is active")
	}
	if mid.Status != RunStatusRunning {
		t.Fatalf("expected run RUNNING, got %s", mid.Status)
	}
	// Handler step should be dispatched (RUNNING) — scheduleReady fires within HandleJobResult.
	if mid.Steps["handler"] == nil {
		t.Fatal("expected handler step to exist")
	}
	handlerStatus := mid.Steps["handler"].Status
	if handlerStatus != StepStatusPending && handlerStatus != StepStatusRunning {
		t.Fatalf("expected handler step PENDING or RUNNING, got %s", handlerStatus)
	}

	// Check timeline for redirect event.
	events, _ := store.ListTimelineEvents(context.Background(), run.ID, 50)
	if !hasTimelineEvent(events, "step_error_redirect") {
		t.Fatal("expected step_error_redirect timeline event")
	}

	// Let the handler succeed.
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-onerr:handler@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run SUCCEEDED after on_error handler, got %s", final.Status)
	}
}

func TestOnError_NotTriggeredOnSuccess(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wfDef := &Workflow{
		ID:    "wf-onerr-ok",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"main": {
				ID:      "main",
				Type:    StepTypeWorker,
				Topic:   "job.default",
				OnError: "handler",
			},
			"handler": {
				ID:    "handler",
				Type:  StepTypeWorker,
				Topic: "job.default",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-onerr-ok",
		WorkflowID: wfDef.ID,
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Main step succeeds — handler should NOT be activated.
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-onerr-ok:main@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})

	final, _ := store.GetRun(context.Background(), run.ID)
	// Handler should not have been activated — run should still resolve.
	// Since handler has no deps and main succeeded, handler might get dispatched normally.
	// But the key test: no step_error_redirect event.
	events, _ := store.ListTimelineEvents(context.Background(), run.ID, 50)
	if hasTimelineEvent(events, "step_error_redirect") {
		t.Fatal("step_error_redirect should NOT fire when main step succeeds")
	}
	_ = final
}

func TestOnError_HandlerFailsCausesRunFailure(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wfDef := &Workflow{
		ID:    "wf-onerr-fail",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"main": {
				ID:      "main",
				Type:    StepTypeWorker,
				Topic:   "job.default",
				OnError: "handler",
			},
			"handler": {
				ID:    "handler",
				Type:  StepTypeWorker,
				Topic: "job.default",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-onerr-fail",
		WorkflowID: wfDef.ID,
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Fail the main step.
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-onerr-fail:main@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "main broke",
	})

	// Now fail the handler too.
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-onerr-fail:handler@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "handler also broke",
	})

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run FAILED when on_error handler itself fails, got %s", final.Status)
	}
}

// --- Crash-recovery / persist-before-dispatch tests ---

// failingBus always returns an error on Publish.
type failingBus struct {
	err error
}

func (b *failingBus) Publish(string, *pb.BusPacket) error { return b.err }
func (b *failingBus) Subscribe(string, string, func(*pb.BusPacket) error) error {
	return nil
}

func TestCrashRecovery_DispatchFailRevertsStepToPending(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &failingBus{err: fmt.Errorf("NATS down")}
	engine := NewEngine(store, bus)

	wfDef := &Workflow{
		ID:    "wf-crash",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step1": {
				ID:    "step1",
				Type:  StepTypeWorker,
				Topic: "job.default",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-crash",
		WorkflowID: wfDef.ID,
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// StartRun calls scheduleReady which will try to publish and fail.
	_ = engine.StartRun(context.Background(), wfDef.ID, run.ID)

	// Step should be reverted to PENDING (not stuck as RUNNING or FAILED).
	final, _ := store.GetRun(context.Background(), run.ID)
	sr := final.Steps["step1"]
	if sr != nil && sr.Status == StepStatusRunning {
		t.Fatal("step should NOT be stuck as RUNNING after dispatch failure")
	}
	// Step may be nil (never set) or PENDING.
	if sr != nil && sr.Status != StepStatusPending && sr.Status != "" {
		t.Fatalf("expected step PENDING or unset after dispatch failure, got %s", sr.Status)
	}
}

func TestCrashRecovery_SuccessfulDispatchPersistsRunningState(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wfDef := &Workflow{
		ID:    "wf-persist",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step1": {
				ID:    "step1",
				Type:  StepTypeWorker,
				Topic: "job.default",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-persist",
		WorkflowID: wfDef.ID,
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Step should be persisted as RUNNING with a JobID.
	final, _ := store.GetRun(context.Background(), run.ID)
	sr := final.Steps["step1"]
	if sr == nil {
		t.Fatal("expected step1 to be present in run")
	}
	if sr.Status != StepStatusRunning {
		t.Fatalf("expected step RUNNING, got %s", sr.Status)
	}
	if sr.JobID == "" {
		t.Fatal("expected step to have a JobID assigned")
	}

	// Verify the dispatched job has an idempotency key.
	msgs := bus.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("expected at least one published message")
	}
	req := msgs[0].packet.GetJobRequest()
	if req == nil {
		t.Fatal("expected job request in published packet")
	}
	if req.Meta == nil || req.Meta.IdempotencyKey == "" {
		t.Fatal("expected idempotency key on dispatched job request")
	}
	expected := fmt.Sprintf("wf:%s:step1:1", run.ID)
	if req.Meta.IdempotencyKey != expected {
		t.Fatalf("expected idempotency key %q, got %q", expected, req.Meta.IdempotencyKey)
	}
}

// TestLockRunCommaOk verifies lockRun doesn't panic when the sync.Map contains
// a value of the wrong type (e.g. a string instead of *runLock).
// TestLockRunAcquireRelease verifies basic acquire/release cycle.
func TestLockRunAcquireRelease(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	unlock, ok := engine.lockRun("run-1")
	if !ok {
		t.Fatal("expected lock acquisition to succeed")
	}
	unlock()

	// Second call should work normally.
	unlock2, ok := engine.lockRun("run-1")
	if !ok {
		t.Fatal("expected second lock acquisition to succeed")
	}
	unlock2()
}

// TestMarkRunTerminalCleanup verifies that markRunTerminal + last unlock
// removes the entry from the lock map.
func TestMarkRunTerminalCleanup(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Acquire the lock.
	unlock, ok := engine.lockRun("run-term")
	if !ok {
		t.Fatal("expected lock acquisition to succeed")
	}

	// Mark terminal while still held — should NOT delete yet because refs > 0.
	engine.markRunTerminal("run-term")

	// Entry should still exist.
	engine.lockMgr.mu.Lock()
	_, exists := engine.lockMgr.locks["run-term"]
	engine.lockMgr.mu.Unlock()
	if !exists {
		t.Fatal("expected entry to still exist while lock is held")
	}

	// Release lock — this decrements refs to 0 and terminal is set, so entry should be deleted.
	unlock()

	// Entry should be gone.
	engine.lockMgr.mu.Lock()
	_, exists = engine.lockMgr.locks["run-term"]
	engine.lockMgr.mu.Unlock()
	if exists {
		t.Fatal("expected entry to be cleaned up after unlock with terminal flag")
	}
}

// TestMarkRunTerminalNoEntry verifies markRunTerminal is safe when no lock exists.
func TestMarkRunTerminalNoEntry(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Should not panic.
	engine.markRunTerminal("nonexistent-run")
	engine.markRunTerminal("")
}

func TestForEach_ExpressionEvaluatedOnce(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	// Limit to 1 parallel to force multiple scheduleReady calls.
	wf := &Workflow{
		ID:    "wf-foreach-once",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"fan": {
				ID:          "fan",
				Type:        StepTypeWorker,
				Topic:       "job.default",
				ForEach:     "input.items",
				MaxParallel: 1,
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-foreach-once",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      map[string]any{"items": []any{"a", "b", "c"}},
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
	// MaxParallel=1 → only 1 dispatched.
	if bus.Count() != 1 {
		t.Fatalf("expected 1 dispatch (max_parallel=1), got %d", bus.Count())
	}

	// Verify ResolvedItems is stored.
	afterStart, _ := store.GetRun(context.Background(), run.ID)
	fanSR := afterStart.Steps["fan"]
	if fanSR == nil {
		t.Fatal("fan step run missing")
	}
	if len(fanSR.ResolvedItems) != 3 {
		t.Fatalf("expected 3 resolved items, got %d", len(fanSR.ResolvedItems))
	}

	// Complete first child → triggers second dispatch via HandleJobResult.
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-foreach-once:fan[0]@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})

	// The second scheduleReady should use stored items, not re-evaluate.
	afterSecond, _ := store.GetRun(context.Background(), run.ID)
	fanSR2 := afterSecond.Steps["fan"]
	if len(fanSR2.ResolvedItems) != 3 {
		t.Fatalf("resolved items should persist, got %d", len(fanSR2.ResolvedItems))
	}
	// At least 2 dispatches now (initial + second child).
	if bus.Count() < 2 {
		t.Fatalf("expected at least 2 dispatches, got %d", bus.Count())
	}
}

func TestForEach_EmptyList_EmitsEvents(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	var finishedSteps []string
	engine.OnStepFinished = func(runID, stepID string, status StepStatus) {
		finishedSteps = append(finishedSteps, stepID)
	}

	wf := &Workflow{
		ID:    "wf-foreach-empty",
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
		ID:         "run-foreach-empty",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      map[string]any{"items": []any{}},
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

	// No jobs should be dispatched.
	if bus.Count() != 0 {
		t.Fatalf("expected 0 dispatches for empty foreach, got %d", bus.Count())
	}

	// Step should auto-succeed.
	final, _ := store.GetRun(context.Background(), run.ID)
	fanSR := final.Steps["fan"]
	if fanSR == nil || fanSR.Status != StepStatusSucceeded {
		t.Fatalf("expected fan step succeeded, got %v", fanSR)
	}

	// Output should be empty list, not nil.
	if fanSR.Output == nil {
		t.Fatal("expected empty list output, got nil")
	}
	if items, ok := fanSR.Output.([]any); !ok || len(items) != 0 {
		t.Fatalf("expected empty []any output, got %v (%T)", fanSR.Output, fanSR.Output)
	}

	// OnStepFinished should have fired.
	if len(finishedSteps) == 0 || finishedSteps[len(finishedSteps)-1] != "fan" {
		t.Fatalf("expected OnStepFinished for 'fan', got %v", finishedSteps)
	}

	// Timeline event should have been emitted.
	if !hasTimelineEventForRun(t, store, run.ID, "step_completed") {
		t.Fatal("expected step_completed timeline event for empty foreach")
	}
}

func TestCondition_FalsePath_EmitsEvents(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	var finishedSteps []string
	engine.OnStepFinished = func(runID, stepID string, status StepStatus) {
		finishedSteps = append(finishedSteps, stepID)
	}

	wf := &Workflow{
		ID:    "wf-cond-false",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"gate": {
				ID:        "gate",
				Type:      StepTypeWorker,
				Topic:     "job.default",
				Condition: "input.enabled == true",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-cond-false",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      map[string]any{"enabled": false},
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

	// No jobs should be dispatched (condition is false).
	if bus.Count() != 0 {
		t.Fatalf("expected 0 dispatches for false condition, got %d", bus.Count())
	}

	// Step should auto-succeed.
	final, _ := store.GetRun(context.Background(), run.ID)
	gateSR := final.Steps["gate"]
	if gateSR == nil || gateSR.Status != StepStatusSucceeded {
		t.Fatalf("expected gate step succeeded, got %v", gateSR)
	}

	// OnStepFinished should have fired.
	found := false
	for _, s := range finishedSteps {
		if s == "gate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected OnStepFinished for 'gate', got %v", finishedSteps)
	}

	// Timeline event should have been emitted.
	if !hasTimelineEventForRun(t, store, run.ID, "step_condition_skipped") {
		t.Fatal("expected step_condition_skipped timeline event for false condition")
	}
}
