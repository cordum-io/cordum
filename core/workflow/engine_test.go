package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	memstore "github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
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

type failingContextStore struct {
	err error
}

func (f *failingContextStore) PutContext(context.Context, string, []byte) error { return f.err }
func (f *failingContextStore) GetContext(context.Context, string) ([]byte, error) {
	return nil, f.err
}
func (f *failingContextStore) PutResult(context.Context, string, []byte) error { return f.err }
func (f *failingContextStore) GetResult(context.Context, string) ([]byte, error) {
	return nil, f.err
}
func (f *failingContextStore) Close() error { return nil }

func TestEngineForEachFanoutAndAggregateSuccess(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 2 {
		t.Fatalf("expected 2 fan-out publishes, got %d", bus.Count())
	}

	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-foreach:fan[0]@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-foreach:fan[1]@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	final, _ := store.GetRun(testCtx(t), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	if final.Steps["fan"].Status != StepStatusSucceeded {
		t.Fatalf("expected parent step succeeded, got %s", final.Steps["fan"].Status)
	}
}

func TestEngineForEachFanoutLimitExceeded(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 0 {
		t.Fatalf("expected no fan-out publishes, got %d", bus.Count())
	}

	final, _ := store.GetRun(testCtx(t), run.ID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	if final.Steps["fan"] == nil || final.Steps["fan"].Status != StepStatusFailed {
		t.Fatalf("expected parent step failed, got %#v", final.Steps["fan"])
	}
	if msg, ok := final.Steps["fan"].Error["message"].(string); !ok || msg == "" {
		t.Fatalf("expected error message on step")
	}

	events, err := store.ListTimelineEvents(testCtx(t), run.ID, 20)
	if err != nil {
		t.Fatalf("list timeline: %v", err)
	}
	if !hasTimelineEvent(events, "step_foreach_failed") {
		t.Fatalf("expected step_foreach_failed timeline event")
	}
}

func TestEngineRetriesAndBackoff(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-retry",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 1 {
		t.Fatalf("expected 1 publish, got %d", bus.Count())
	}

	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-retry:step@1",
		Status: pb.JobStatus_JOB_STATUS_FAILED,
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

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

	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-retry:step@2",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}
	final, _ := store.GetRun(testCtx(t), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded after retry, got %s", final.Status)
	}
}

func TestEngineStepMetadataPropagates(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-meta",
		WorkflowID: wf.ID,
		OrgID:      "tenant-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
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
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-delay",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"wait": {ID: "wait", Type: StepTypeDelay, DelaySec: 1},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-delay",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 0 {
		t.Fatalf("expected no publishes for delay step, got %d", bus.Count())
	}

	// Poll until the delay step completes and the run succeeds.
	deadline := time.Now().Add(5 * time.Second)
	var final *WorkflowRun
	for time.Now().Before(deadline) {
		final, _ = store.GetRun(testCtx(t), run.ID)
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
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-notify",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"notify": {ID: "notify", Type: StepTypeNotify, Input: map[string]any{"message": "hello"}},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-notify",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
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
	final, _ := store.GetRun(testCtx(t), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded after notify, got %s", final.Status)
	}
}

func TestEngineConditionStepEvaluates(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-condition",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"cond": {ID: "cond", Type: StepTypeCondition, Condition: "input.allow", OutputPath: "decision.allowed"},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final, _ := store.GetRun(testCtx(t), run.ID)
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
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-condition-error",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.default", Condition: "!"},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-condition-error",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 0 {
		t.Fatalf("expected no dispatches on condition eval error, got %d", bus.Count())
	}

	final, _ := store.GetRun(testCtx(t), run.ID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	if final.Steps["step"] == nil || final.Steps["step"].Status != StepStatusFailed {
		t.Fatalf("expected step failed, got %#v", final.Steps["step"])
	}
	if msg, ok := final.Steps["step"].Error["message"].(string); !ok || msg == "" {
		t.Fatalf("expected error message on step")
	}

	events, err := store.ListTimelineEvents(testCtx(t), run.ID, 20)
	if err != nil {
		t.Fatalf("list timeline: %v", err)
	}
	if !hasTimelineEvent(events, "step_condition_failed") {
		t.Fatalf("expected step_condition_failed timeline event")
	}
}

func TestEngineForEachEvalErrorFailsRun(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-foreach-error",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"fan": {ID: "fan", Type: StepTypeWorker, Topic: "job.default", ForEach: "input.value"},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 0 {
		t.Fatalf("expected no dispatches on for_each eval error, got %d", bus.Count())
	}

	final, _ := store.GetRun(testCtx(t), run.ID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	if final.Steps["fan"] == nil || final.Steps["fan"].Status != StepStatusFailed {
		t.Fatalf("expected step failed, got %#v", final.Steps["fan"])
	}

	events, err := store.ListTimelineEvents(testCtx(t), run.ID, 20)
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
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Directly test scheduleAfter with a short delay.
	engine.scheduleAfter(50*time.Millisecond, wf.ID, run.ID)
	if n := engine.PendingTimers(); n != 1 {
		t.Fatalf("expected 1 pending timer, got %d", n)
	}

	// Poll until the timer fires.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if engine.PendingTimers() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := engine.PendingTimers(); n != 0 {
		t.Fatalf("expected 0 pending timers after fire, got %d", n)
	}
}

func TestScheduleAfterMultipleTimers(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	defer engine.Stop()

	wf := &Workflow{
		ID:    "wf-multi-timer",
		OrgID: "org-1",
		Steps: map[string]*Step{},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
		if err := store.CreateRun(testCtx(t), run); err != nil {
			t.Fatalf("create run: %v", err)
		}
		engine.scheduleAfter(50*time.Millisecond, wf.ID, runID)
	}

	if n := engine.PendingTimers(); n != 5 {
		t.Fatalf("expected 5 pending timers, got %d", n)
	}

	// Poll until all timers fire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if engine.PendingTimers() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := engine.PendingTimers(); n != 0 {
		t.Fatalf("expected 0 pending timers, got %d", n)
	}
}

func TestStopCancelsPendingTimers(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-stop-cancel",
		OrgID: "org-1",
		Steps: map[string]*Step{},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
		if err := store.CreateRun(testCtx(t), run); err != nil {
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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wfDef); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Fail the main step.
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:        "run-onerr:main@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "something broke",
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	// Run should NOT be failed yet — on_error handler should be dispatched.
	mid, _ := store.GetRun(testCtx(t), run.ID)
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
	events, _ := store.ListTimelineEvents(testCtx(t), run.ID, 50)
	if !hasTimelineEvent(events, "step_error_redirect") {
		t.Fatal("expected step_error_redirect timeline event")
	}

	// Let the handler succeed.
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-onerr:handler@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	final, _ := store.GetRun(testCtx(t), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run SUCCEEDED after on_error handler, got %s", final.Status)
	}
}

func TestOnError_NotTriggeredOnSuccess(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wfDef); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Main step succeeds — handler should NOT be activated.
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-onerr-ok:main@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	final, _ := store.GetRun(testCtx(t), run.ID)
	// Handler should not have been activated — run should still resolve.
	// Since handler has no deps and main succeeded, handler might get dispatched normally.
	// But the key test: no step_error_redirect event.
	events, _ := store.ListTimelineEvents(testCtx(t), run.ID, 50)
	if hasTimelineEvent(events, "step_error_redirect") {
		t.Fatal("step_error_redirect should NOT fire when main step succeeds")
	}
	_ = final
}

func TestOnError_HandlerFailsCausesRunFailure(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wfDef); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Fail the main step.
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:        "run-onerr-fail:main@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "main broke",
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	// Now fail the handler too.
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:        "run-onerr-fail:handler@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "handler also broke",
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	final, _ := store.GetRun(testCtx(t), run.ID)
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
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wfDef); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// StartRun calls scheduleReady which will try to publish and fail.
	_ = engine.StartRun(testCtx(t), wfDef.ID, run.ID)

	// Step should be reverted to PENDING (not stuck as RUNNING or FAILED).
	final, _ := store.GetRun(testCtx(t), run.ID)
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
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wfDef); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Step should be persisted as RUNNING with a JobID.
	final, _ := store.GetRun(testCtx(t), run.ID)
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

func TestWorkflowApprovalStepPersistsStructuredContext(t *testing.T) {
	wfStore := newWorkflowStore(t)
	defer func() { _ = wfStore.Close() }()

	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(wfStore, bus).WithMemory(memStore)

	wfDef := &Workflow{
		ID:    "wf-approval-rich",
		OrgID: "org-1",
		Name:  "Vendor approvals",
		Steps: map[string]*Step{
			"approve": {
				ID:   "approve",
				Name: "Manager Approval",
				Type: StepTypeApproval,
				Input: map[string]any{
					"amount":            "${input.request.amount}",
					"currency":          "${input.request.currency}",
					"vendor":            "${input.request.vendor.name}",
					"items":             "${input.request.items}",
					"escalation_reason": "${steps.risk.output.reason}",
					"optional_note":     "${input.request.note}",
				},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"amount":   map[string]any{"type": "number"},
						"currency": map[string]any{"type": "string"},
						"vendor":   map[string]any{"type": "string"},
						"items":    map[string]any{"type": "array"},
					},
					"required": []any{"amount", "currency", "vendor", "items"},
				},
			},
		},
	}
	if err := wfStore.SaveWorkflow(testCtx(t), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	// This run models the production contract later consumed by the approvals
	// API/UI: workflow metadata plus structured decision data (amount, vendor,
	// items, escalation reason), with missing optional fields omitted entirely.
	run := &WorkflowRun{
		ID:          "run-approval-rich",
		WorkflowID:  wfDef.ID,
		OrgID:       "org-1",
		TeamID:      "team-1",
		TriggeredBy: "alice",
		Input: map[string]any{
			"request": map[string]any{
				"amount":   4200,
				"currency": "USD",
				"vendor":   map[string]any{"name": "Acme Travel"},
				"items": []any{
					map[string]any{"sku": "flight", "price": 4200},
				},
			},
		},
		Context: map[string]any{
			"steps": map[string]any{
				"risk": map[string]any{
					"output": map[string]any{
						"reason": "manager threshold exceeded",
					},
				},
			},
		},
		Status:    RunStatusPending,
		Steps:     map[string]*StepRun{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := wfStore.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 1 {
		t.Fatalf("expected one approval dispatch, got %d", bus.Count())
	}

	final, err := wfStore.GetRun(testCtx(t), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	sr := final.Steps["approve"]
	if sr == nil {
		t.Fatal("expected approval step state")
	}
	if sr.Status != StepStatusWaiting {
		t.Fatalf("expected approval step waiting, got %s", sr.Status)
	}
	if final.Status != RunStatusWaiting {
		t.Fatalf("expected run waiting, got %s", final.Status)
	}

	req := bus.Snapshot()[0].packet.GetJobRequest()
	if req == nil {
		t.Fatal("expected approval job request")
	}
	if req.Topic != capsdk.SubjectWorkflowApprovalGate {
		t.Fatalf("expected workflow approval topic, got %q", req.Topic)
	}
	if req.ContextPtr == "" {
		t.Fatal("expected context_ptr on approval job request")
	}
	if got := req.Labels["gate_type"]; got != "workflow_approval" {
		t.Fatalf("expected gate_type workflow_approval, got %q", got)
	}

	key, err := memstore.KeyFromPointer(req.ContextPtr)
	if err != nil {
		t.Fatalf("parse context ptr: %v", err)
	}
	raw, err := memStore.GetContext(testCtx(t), key)
	if err != nil {
		t.Fatalf("fetch approval context: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode approval payload: %v", err)
	}
	if payload["kind"] != ApprovalContextKindWorkflow {
		t.Fatalf("expected workflow approval payload kind, got %#v", payload["kind"])
	}
	if payload["version"] != float64(ApprovalContextVersionV1) {
		t.Fatalf("expected approval payload version %d, got %#v", ApprovalContextVersionV1, payload["version"])
	}
	workflowMeta, ok := payload["workflow"].(map[string]any)
	if !ok {
		t.Fatalf("expected workflow metadata object, got %T", payload["workflow"])
	}
	if workflowMeta["workflow_id"] != wfDef.ID || workflowMeta["run_id"] != run.ID {
		t.Fatalf("unexpected workflow metadata: %#v", workflowMeta)
	}
	if workflowMeta["step_name"] != "Manager Approval" {
		t.Fatalf("expected step_name Manager Approval, got %#v", workflowMeta["step_name"])
	}
	if workflowMeta["triggered_by"] != "alice" {
		t.Fatalf("expected triggered_by alice, got %#v", workflowMeta["triggered_by"])
	}
	decision, ok := payload["decision"].(map[string]any)
	if !ok {
		t.Fatalf("expected decision object, got %T", payload["decision"])
	}
	if decision["vendor"] != "Acme Travel" {
		t.Fatalf("expected vendor Acme Travel, got %#v", decision["vendor"])
	}
	if decision["escalation_reason"] != "manager threshold exceeded" {
		t.Fatalf("expected escalation reason, got %#v", decision["escalation_reason"])
	}
	if _, exists := decision["optional_note"]; exists {
		t.Fatalf("optional_note should be omitted when missing, got %#v", decision["optional_note"])
	}
	if !reflect.DeepEqual(sr.Input, payload) {
		t.Fatalf("step input should match persisted approval payload\nstep=%#v\npayload=%#v", sr.Input, payload)
	}
}

func TestWorkflowApprovalStepSupportsLegacyMetadataOnlyPayload(t *testing.T) {
	wfStore := newWorkflowStore(t)
	defer func() { _ = wfStore.Close() }()

	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(wfStore, bus).WithMemory(memStore)

	wfDef := &Workflow{
		ID:    "wf-approval-legacy",
		OrgID: "org-1",
		Name:  "Legacy approvals",
		Steps: map[string]*Step{
			"approve": {ID: "approve", Type: StepTypeApproval},
		},
	}
	if err := wfStore.SaveWorkflow(testCtx(t), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-approval-legacy",
		WorkflowID: wfDef.ID,
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := wfStore.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	req := bus.Snapshot()[0].packet.GetJobRequest()
	// Legacy approval steps without explicit input must still get a stable
	// envelope so downstream consumers can identify the workflow gate without
	// accidentally inheriting the whole workflow run input.
	key, err := memstore.KeyFromPointer(req.ContextPtr)
	if err != nil {
		t.Fatalf("parse context ptr: %v", err)
	}
	raw, err := memStore.GetContext(testCtx(t), key)
	if err != nil {
		t.Fatalf("fetch approval context: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode approval payload: %v", err)
	}
	if _, exists := payload["decision"]; exists {
		t.Fatalf("legacy approval payload should omit decision block, got %#v", payload["decision"])
	}
	workflowMeta := payload["workflow"].(map[string]any)
	if workflowMeta["step_id"] != "approve" {
		t.Fatalf("expected step_id approve, got %#v", workflowMeta["step_id"])
	}
}

func TestWorkflowApprovalStepFailsWhenRequiredDecisionFieldMissing(t *testing.T) {
	wfStore := newWorkflowStore(t)
	defer func() { _ = wfStore.Close() }()

	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(wfStore, bus).WithMemory(memStore)

	wfDef := &Workflow{
		ID:    "wf-approval-missing",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"approve": {
				ID:   "approve",
				Type: StepTypeApproval,
				Input: map[string]any{
					"amount":   "${input.request.amount}",
					"currency": "${input.request.currency}",
				},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"amount":   map[string]any{"type": "number"},
						"currency": map[string]any{"type": "string"},
					},
					"required": []any{"amount", "currency"},
				},
			},
		},
	}
	if err := wfStore.SaveWorkflow(testCtx(t), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-approval-missing",
		WorkflowID: wfDef.ID,
		OrgID:      "org-1",
		Input: map[string]any{
			"request": map[string]any{"amount": 10},
		},
		Status:    RunStatusPending,
		Steps:     map[string]*StepRun{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := wfStore.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 0 {
		t.Fatalf("expected no approval dispatch on validation failure, got %d", bus.Count())
	}

	final, err := wfStore.GetRun(testCtx(t), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	sr := final.Steps["approve"]
	if sr == nil || sr.Status != StepStatusFailed {
		t.Fatalf("expected failed approval step, got %#v", sr)
	}
	msg, _ := sr.Error["message"].(string)
	if !strings.Contains(msg, "currency") || !strings.Contains(msg, "resolved to nil") {
		t.Fatalf("expected explicit missing currency error, got %q", msg)
	}
}

func TestWorkflowApprovalStepFailsWhenContextStoreUnavailable(t *testing.T) {
	wfStore := newWorkflowStore(t)
	defer func() { _ = wfStore.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(wfStore, bus).WithMemory(&failingContextStore{err: errors.New("context store unavailable")})

	wfDef := &Workflow{
		ID:    "wf-approval-store-fail",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"approve": {
				ID:   "approve",
				Type: StepTypeApproval,
				Input: map[string]any{
					"amount": "${input.amount}",
				},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"amount": map[string]any{"type": "number"},
					},
					"required": []any{"amount"},
				},
			},
		},
	}
	if err := wfStore.SaveWorkflow(testCtx(t), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-approval-store-fail",
		WorkflowID: wfDef.ID,
		OrgID:      "org-1",
		Input:      map[string]any{"amount": 99},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := wfStore.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 0 {
		t.Fatalf("expected no approval dispatch when context store fails, got %d", bus.Count())
	}

	final, err := wfStore.GetRun(testCtx(t), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	sr := final.Steps["approve"]
	if sr == nil || sr.Status != StepStatusFailed {
		t.Fatalf("expected failed approval step, got %#v", sr)
	}
	msg, _ := sr.Error["message"].(string)
	if !strings.Contains(msg, "context store unavailable") {
		t.Fatalf("expected context store failure in error, got %q", msg)
	}
	if sr.Input == nil {
		t.Fatal("expected failed approval step to retain payload for debugging")
	}
}

func TestWorkflowApprovalStepApproveResultAdvancesRunAndPreservesContext(t *testing.T) {
	wfStore := newWorkflowStore(t)
	defer func() { _ = wfStore.Close() }()

	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(wfStore, bus).WithMemory(memStore)

	wfDef := &Workflow{
		ID:    "wf-approval-approve",
		OrgID: "org-1",
		Name:  "Expense approvals",
		Steps: map[string]*Step{
			"approve": {
				ID:   "approve",
				Name: "Finance Approval",
				Type: StepTypeApproval,
				Input: map[string]any{
					"amount":   "${input.request.amount}",
					"currency": "${input.request.currency}",
					"vendor":   "${input.request.vendor}",
				},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"amount":   map[string]any{"type": "number"},
						"currency": map[string]any{"type": "string"},
						"vendor":   map[string]any{"type": "string"},
					},
					"required": []any{"amount", "currency", "vendor"},
				},
			},
			"settle": {
				ID:        "settle",
				Type:      StepTypeWorker,
				Topic:     "job.finance.settle",
				DependsOn: []string{"approve"},
			},
		},
	}
	if err := wfStore.SaveWorkflow(testCtx(t), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:          "run-approval-approve",
		WorkflowID:  wfDef.ID,
		OrgID:       "org-1",
		TeamID:      "team-1",
		TriggeredBy: "alice",
		Input: map[string]any{
			"request": map[string]any{
				"amount":   1250,
				"currency": "USD",
				"vendor":   "Acme Travel",
			},
		},
		Status:    RunStatusPending,
		Steps:     map[string]*StepRun{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := wfStore.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 1 {
		t.Fatalf("expected approval gate dispatch, got %d", bus.Count())
	}

	req := bus.Snapshot()[0].packet.GetJobRequest()
	if req == nil || req.ContextPtr == "" {
		t.Fatal("expected approval gate request with context_ptr")
	}
	key, err := memstore.KeyFromPointer(req.ContextPtr)
	if err != nil {
		t.Fatalf("parse context ptr: %v", err)
	}
	raw, err := memStore.GetContext(testCtx(t), key)
	if err != nil {
		t.Fatalf("fetch approval context: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode approval payload: %v", err)
	}

	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  req.JobId,
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("handle approval result: %v", err)
	}
	if bus.Count() != 2 {
		t.Fatalf("expected downstream dispatch after approval, got %d", bus.Count())
	}

	afterApprove, err := wfStore.GetRun(testCtx(t), run.ID)
	if err != nil {
		t.Fatalf("get run after approve: %v", err)
	}
	approveSR := afterApprove.Steps["approve"]
	if approveSR == nil || approveSR.Status != StepStatusSucceeded {
		t.Fatalf("expected approval step succeeded, got %#v", approveSR)
	}
	if !reflect.DeepEqual(approveSR.Input, payload) {
		t.Fatalf("approval step should retain persisted context\nstep=%#v\npayload=%#v", approveSR.Input, payload)
	}
	if afterApprove.Status != RunStatusRunning {
		t.Fatalf("expected run running after approval, got %s", afterApprove.Status)
	}
	settleSR := afterApprove.Steps["settle"]
	if settleSR == nil || settleSR.Status != StepStatusRunning {
		t.Fatalf("expected settle step running, got %#v", settleSR)
	}
	if settleSR.JobID == "" {
		t.Fatal("expected settle step job id after approval")
	}

	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  settleSR.JobID,
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("handle settle result: %v", err)
	}

	final, err := wfStore.GetRun(testCtx(t), run.ID)
	if err != nil {
		t.Fatalf("get final run: %v", err)
	}
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected final run succeeded, got %s", final.Status)
	}
}

func TestWorkflowApprovalStepDeniedResultStopsRunAndPreservesContext(t *testing.T) {
	wfStore := newWorkflowStore(t)
	defer func() { _ = wfStore.Close() }()

	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(wfStore, bus).WithMemory(memStore)

	wfDef := &Workflow{
		ID:    "wf-approval-denied",
		OrgID: "org-1",
		Name:  "Expense approvals",
		Steps: map[string]*Step{
			"approve": {
				ID:   "approve",
				Name: "Finance Approval",
				Type: StepTypeApproval,
				Input: map[string]any{
					"amount":   "${input.request.amount}",
					"currency": "${input.request.currency}",
					"vendor":   "${input.request.vendor}",
				},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"amount":   map[string]any{"type": "number"},
						"currency": map[string]any{"type": "string"},
						"vendor":   map[string]any{"type": "string"},
					},
					"required": []any{"amount", "currency", "vendor"},
				},
			},
			"settle": {
				ID:        "settle",
				Type:      StepTypeWorker,
				Topic:     "job.finance.settle",
				DependsOn: []string{"approve"},
			},
		},
	}
	if err := wfStore.SaveWorkflow(testCtx(t), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-approval-denied",
		WorkflowID: wfDef.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input: map[string]any{
			"request": map[string]any{
				"amount":   9800,
				"currency": "USD",
				"vendor":   "Contoso Travel",
			},
		},
		Status:    RunStatusPending,
		Steps:     map[string]*StepRun{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := wfStore.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 1 {
		t.Fatalf("expected approval gate dispatch, got %d", bus.Count())
	}

	req := bus.Snapshot()[0].packet.GetJobRequest()
	if req == nil || req.ContextPtr == "" {
		t.Fatal("expected approval gate request with context_ptr")
	}
	key, err := memstore.KeyFromPointer(req.ContextPtr)
	if err != nil {
		t.Fatalf("parse context ptr: %v", err)
	}
	raw, err := memStore.GetContext(testCtx(t), key)
	if err != nil {
		t.Fatalf("fetch approval context: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode approval payload: %v", err)
	}

	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:        req.JobId,
		Status:       pb.JobStatus_JOB_STATUS_DENIED,
		ErrorMessage: "manager rejected vendor risk",
	}); err != nil {
		t.Fatalf("handle denied approval result: %v", err)
	}
	if bus.Count() != 1 {
		t.Fatalf("expected no downstream dispatch after denial, got %d", bus.Count())
	}

	final, err := wfStore.GetRun(testCtx(t), run.ID)
	if err != nil {
		t.Fatalf("get final run: %v", err)
	}
	if final.Status != RunStatusDenied {
		t.Fatalf("expected denied run, got %s", final.Status)
	}
	approveSR := final.Steps["approve"]
	if approveSR == nil || approveSR.Status != StepStatusDenied {
		t.Fatalf("expected denied approval step, got %#v", approveSR)
	}
	if approveSR.Error["message"] != "manager rejected vendor risk" {
		t.Fatalf("expected denial reason to be preserved, got %#v", approveSR.Error)
	}
	if !reflect.DeepEqual(approveSR.Input, payload) {
		t.Fatalf("approval step should retain persisted context after denial\nstep=%#v\npayload=%#v", approveSR.Input, payload)
	}
	if settleSR := final.Steps["settle"]; settleSR != nil {
		t.Fatalf("expected downstream step not to dispatch after denial, got %#v", settleSR)
	}
}

// TestLockRunCommaOk verifies lockRun doesn't panic when the sync.Map contains
// a value of the wrong type (e.g. a string instead of *runLock).
// TestLockRunAcquireRelease verifies basic acquire/release cycle.
func TestLockRunAcquireRelease(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Should not panic.
	engine.markRunTerminal("nonexistent-run")
	engine.markRunTerminal("")
}

func TestForEach_ExpressionEvaluatedOnce(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	// MaxParallel=1 → only 1 dispatched.
	if bus.Count() != 1 {
		t.Fatalf("expected 1 dispatch (max_parallel=1), got %d", bus.Count())
	}

	// Verify ResolvedItems is stored.
	afterStart, _ := store.GetRun(testCtx(t), run.ID)
	fanSR := afterStart.Steps["fan"]
	if fanSR == nil {
		t.Fatal("fan step run missing")
	}
	if len(fanSR.ResolvedItems) != 3 {
		t.Fatalf("expected 3 resolved items, got %d", len(fanSR.ResolvedItems))
	}

	// Complete first child → triggers second dispatch via HandleJobResult.
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-foreach-once:fan[0]@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	// The second scheduleReady should use stored items, not re-evaluate.
	afterSecond, _ := store.GetRun(testCtx(t), run.ID)
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
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// No jobs should be dispatched.
	if bus.Count() != 0 {
		t.Fatalf("expected 0 dispatches for empty foreach, got %d", bus.Count())
	}

	// Step should auto-succeed.
	final, _ := store.GetRun(testCtx(t), run.ID)
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
	defer func() { _ = store.Close() }()

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
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
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
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// No jobs should be dispatched (condition is false).
	if bus.Count() != 0 {
		t.Fatalf("expected 0 dispatches for false condition, got %d", bus.Count())
	}

	// Step should auto-succeed.
	final, _ := store.GetRun(testCtx(t), run.ID)
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

// ---------------------------------------------------------------------------
// Regression: HandleJobResult returns ErrRunNotFound for deleted runs
// ---------------------------------------------------------------------------

func TestHandleJobResultDeletedRunReturnsErrRunNotFound(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisWorkflowStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Create workflow + run, start it, then delete the run.
	wfDef := &Workflow{
		ID: "wf-deleted",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID: "run-deleted", WorkflowID: wfDef.ID,
		Steps: map[string]*StepRun{}, Status: RunStatusPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Delete the run.
	if err := store.DeleteRun(testCtx(t), run.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}

	// Now send a job result for the deleted run — should return ErrRunNotFound.
	resultErr := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-deleted:step@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	if resultErr == nil {
		t.Fatal("expected ErrRunNotFound for deleted run, got nil")
	}
	if !errors.Is(resultErr, ErrRunNotFound) {
		t.Fatalf("expected ErrRunNotFound, got: %v", resultErr)
	}
}

func TestHandleJobResultExistingRunReturnsNil(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisWorkflowStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wfDef := &Workflow{
		ID: "wf-exists",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID: "run-exists", WorkflowID: wfDef.ID,
		Steps: map[string]*StepRun{}, Status: RunStatusPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Job result for existing run — should return nil (success).
	resultErr := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-exists:step@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	if resultErr != nil {
		t.Fatalf("expected nil for existing run, got: %v", resultErr)
	}
}

func TestHandleJobResultTransientRedisErrorIsNotErrRunNotFound(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisWorkflowStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Create workflow + run, start it, then close Redis to simulate transient failure.
	wfDef := &Workflow{
		ID: "wf-transient",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID: "run-transient", WorkflowID: wfDef.ID,
		Steps: map[string]*StepRun{}, Status: RunStatusPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(testCtx(t), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Close miniredis to simulate transient Redis failure.
	srv.Close()

	// HandleJobResult should return an error that is NOT ErrRunNotFound.
	resultErr := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-transient:step@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	if resultErr == nil {
		t.Fatal("expected error on Redis failure, got nil")
	}
	if errors.Is(resultErr, ErrRunNotFound) {
		t.Fatalf("transient Redis error must NOT be ErrRunNotFound, got: %v", resultErr)
	}
}

func TestActivateOnErrorHandlerNilRun(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	now := time.Now().UTC()

	// Calling with nil run should not panic.
	engine.activateOnErrorHandler(testCtx(t), nil, &Workflow{}, "step-1", &StepRun{}, now)
	// Calling with nil wfDef should not panic.
	engine.activateOnErrorHandler(testCtx(t), &WorkflowRun{}, nil, "step-1", &StepRun{}, now)
	// Calling with nil stepRun should not panic.
	engine.activateOnErrorHandler(testCtx(t), &WorkflowRun{}, &Workflow{}, "step-1", nil, now)
}

func TestSetContextPathErrorLogged(t *testing.T) {
	// setContextPath with valid path should succeed.
	ctx := map[string]any{}
	if err := setContextPath(ctx, "steps.output", "value"); err != nil {
		t.Fatalf("expected no error for valid path, got: %v", err)
	}
	if ctx["steps"] == nil {
		t.Fatal("expected steps key to be set")
	}

	// setContextPath with empty segment should return error.
	if err := setContextPath(ctx, "steps..invalid", "value"); err == nil {
		t.Fatal("expected error for path with empty segment")
	}

	// setContextPath with nil ctx should be a no-op.
	if err := setContextPath(nil, "key", "value"); err != nil {
		t.Fatalf("expected no error for nil ctx, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Denied job result → RunStatusDenied regression tests
// ---------------------------------------------------------------------------

func TestEngineDeniedJobResult_ProducesRunDenied(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-denied-e2e",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step": {
				ID:    "step",
				Type:  StepTypeWorker,
				Topic: "job.denied",
			},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-denied-e2e",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 1 {
		t.Fatalf("expected 1 publish, got %d", bus.Count())
	}

	// Deliver a DENIED result for the step job.
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:        "run-denied-e2e:step@1",
		Status:       pb.JobStatus_JOB_STATUS_DENIED,
		ErrorMessage: "denied by safety policy",
	}); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	final, err := store.GetRun(testCtx(t), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Status != RunStatusDenied {
		t.Fatalf("expected run status denied, got %s", final.Status)
	}
	if final.Steps["step"].Status != StepStatusDenied {
		t.Fatalf("expected step status denied, got %s", final.Steps["step"].Status)
	}
	errMsg, _ := final.Steps["step"].Error["message"].(string)
	if errMsg != "denied by safety policy" {
		t.Fatalf("expected error message 'denied by safety policy', got %q", errMsg)
	}
}

func TestEngineDeniedJobResult_WithOnError_Recovers(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-denied-recover-e2e",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"main":     {ID: "main", Type: StepTypeWorker, Topic: "job.main", OnError: "fallback"},
			"fallback": {ID: "fallback", Type: StepTypeWorker, Topic: "job.fallback"},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-denied-recover-e2e",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if bus.Count() != 1 {
		t.Fatalf("expected 1 publish for main step, got %d", bus.Count())
	}

	// Deliver DENIED for the main step.
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:        "run-denied-recover-e2e:main@1",
		Status:       pb.JobStatus_JOB_STATUS_DENIED,
		ErrorMessage: "denied by policy",
	}); err != nil {
		t.Fatalf("handle denied result: %v", err)
	}

	// The on_error handler (fallback) should now be dispatched.
	// Give a brief window for async dispatch if needed.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if bus.Count() >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if bus.Count() < 2 {
		t.Fatalf("expected fallback step dispatch, got %d publishes", bus.Count())
	}

	// Let the fallback handler succeed.
	if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
		JobId:  "run-denied-recover-e2e:fallback@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("handle fallback result: %v", err)
	}

	final, err := store.GetRun(testCtx(t), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	// The on_error handler recovered — run should succeed.
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run status succeeded after on_error recovery, got %s", final.Status)
	}
}

// ---------------------------------------------------------------------------
// HandleJobResult — UpdateRun failure propagation tests
// ---------------------------------------------------------------------------
//
// These tests verify that HandleJobResult returns an error (not nil) when
// the final UpdateRun call fails, so the NATS bus NACKs the message for
// redelivery instead of silently losing the state change.

// TestHandleJobResult_UpdateRunFailure_ReturnsError verifies that a persistent
// UpdateRun failure causes HandleJobResult to return an error.
func TestHandleJobResult_UpdateRunFailure_ReturnsError(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	defer engine.Stop()

	ctx := testCtx(t)

	wf := &Workflow{
		ID:    "wf-update-fail",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step1": {ID: "step1", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-update-fail",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step1": {StepID: "step1", Status: StepStatusRunning, JobID: "run-update-fail:step1@1"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Inject Redis error after step processing completes but before UpdateRun.
	engine.OnStepFinished = func(runID, stepID string, status StepStatus) {
		srv.SetError("INJECTED: persistent store failure")
	}

	hjrErr := engine.HandleJobResult(ctx, &pb.JobResult{
		JobId:  "run-update-fail:step1@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})

	srv.SetError("") // clear for cleanup

	if hjrErr == nil {
		t.Fatal("expected error from HandleJobResult when UpdateRun fails, got nil")
	}
	if !strings.Contains(hjrErr.Error(), "update run after job result") {
		t.Fatalf("expected wrapped UpdateRun error, got: %v", hjrErr)
	}
}

// TestHandleJobResult_UpdateRunFailure_Idempotent verifies that when UpdateRun
// fails (state not persisted), redelivering the same job result processes it
// correctly on the second attempt without double-processing.
func TestHandleJobResult_UpdateRunFailure_Idempotent(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	defer engine.Stop()

	ctx := testCtx(t)

	wf := &Workflow{
		ID:    "wf-idempotent",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step1": {ID: "step1", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-idempotent",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step1": {StepID: "step1", Status: StepStatusRunning, JobID: "run-idempotent:step1@1"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	jobResult := &pb.JobResult{
		JobId:  "run-idempotent:step1@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}

	// First attempt: inject failure so UpdateRun doesn't persist.
	failOnce := true
	engine.OnStepFinished = func(runID, stepID string, status StepStatus) {
		if failOnce {
			srv.SetError("INJECTED: transient store failure")
			failOnce = false
		}
	}

	err := engine.HandleJobResult(ctx, jobResult)
	srv.SetError("") // clear error for second attempt

	if err == nil {
		t.Fatal("first call should have returned error")
	}

	// Verify the run was NOT updated in the store (still Running, step still Running).
	preRun, getErr := store.GetRun(ctx, run.ID)
	if getErr != nil {
		t.Fatalf("get run after failed update: %v", getErr)
	}
	if preRun.Steps["step1"].Status != StepStatusRunning {
		t.Fatalf("step should still be Running after failed UpdateRun, got %s", preRun.Steps["step1"].Status)
	}

	// Second attempt: same job result, store now healthy.
	// OnStepFinished won't inject error this time (failOnce is false).
	err = engine.HandleJobResult(ctx, jobResult)
	if err != nil {
		t.Fatalf("second call should succeed, got: %v", err)
	}

	// Verify state was persisted on second attempt.
	finalRun, getErr := store.GetRun(ctx, run.ID)
	if getErr != nil {
		t.Fatalf("get run after successful update: %v", getErr)
	}
	if finalRun.Steps["step1"].Status != StepStatusSucceeded {
		t.Fatalf("step should be Succeeded after redelivery, got %s", finalRun.Steps["step1"].Status)
	}
	if finalRun.Status != RunStatusSucceeded {
		t.Fatalf("run should be Succeeded after single-step workflow completes, got %s", finalRun.Status)
	}
}

// TestHandleJobResult_UpdateRunRetry_Succeeds verifies the built-in retry:
// if UpdateRun fails transiently on the first attempt but recovers on the
// second, HandleJobResult returns nil (success) and the state is persisted.
func TestHandleJobResult_UpdateRunRetry_Succeeds(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	defer engine.Stop()

	ctx := testCtx(t)

	wf := &Workflow{
		ID:    "wf-retry-ok",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step1": {ID: "step1", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-retry-ok",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step1": {StepID: "step1", Status: StepStatusRunning, JobID: "run-retry-ok:step1@1"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Inject a transient error that clears itself before the retry attempt.
	engine.OnStepFinished = func(runID, stepID string, status StepStatus) {
		srv.SetError("INJECTED: transient blip")
		go func() {
			time.Sleep(100 * time.Millisecond)
			srv.SetError("")
		}()
	}

	err := engine.HandleJobResult(ctx, &pb.JobResult{
		JobId:  "run-retry-ok:step1@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	if err != nil {
		t.Fatalf("expected HandleJobResult to succeed after retry, got: %v", err)
	}

	// Verify state was persisted.
	finalRun, getErr := store.GetRun(ctx, run.ID)
	if getErr != nil {
		t.Fatalf("get run: %v", getErr)
	}
	if finalRun.Steps["step1"].Status != StepStatusSucceeded {
		t.Fatalf("step should be Succeeded after retry, got %s", finalRun.Steps["step1"].Status)
	}
	if finalRun.Status != RunStatusSucceeded {
		t.Fatalf("run should be Succeeded, got %s", finalRun.Status)
	}
}

// ---------------------------------------------------------------------------
// Timer concurrent fire stress test
// ---------------------------------------------------------------------------

// TestTimerConcurrentFireAndRemove launches 100 goroutines that each schedule
// a 1ms timer, verifying no panics or deadlocks from concurrent map access.
// The timers fire almost immediately and remove themselves from pendingTimers.
func TestTimerConcurrentFireAndRemove(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	engine := NewEngine(store, nil)
	defer engine.Stop()

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			// Schedule a very short timer — it will fire almost immediately.
			// StartRun will fail (workflow doesn't exist) but that's fine;
			// the timer callback still removes itself from the map.
			engine.scheduleAfter(
				time.Millisecond,
				fmt.Sprintf("wf-stress-%d", idx),
				fmt.Sprintf("run-stress-%d", idx),
			)
		}(i)
	}

	wg.Wait()

	// Give all timers time to fire and clean up.
	// Each timer has 1ms delay + StartRun attempt (~instant failure).
	time.Sleep(200 * time.Millisecond)

	pending := engine.PendingTimers()
	if pending != 0 {
		t.Fatalf("expected 0 pending timers after all fired, got %d", pending)
	}
}

// ---------------------------------------------------------------------------
// Output validation — ResultPtr preserved on failure
// ---------------------------------------------------------------------------

// TestOutputValidationFailure_PreservesResultPtr verifies that when output
// validation fails, the step record still has the ResultPtr (via Output field)
// and the result is recorded for quarantine inspection.
func TestOutputValidationFailure_PreservesResultPtr(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	defer engine.Stop()

	// Use a nil memory store — fetchResultPayload will fail,
	// triggering "output schema validation failed" (fail-closed).

	ctx := testCtx(t)

	wf := &Workflow{
		ID:    "wf-output-val",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step1": {
				ID:             "step1",
				Type:           StepTypeWorker,
				Topic:          "job.default",
				OutputSchemaID: "schema-must-validate",
			},
		},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-output-val",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step1": {StepID: "step1", Status: StepStatusRunning, JobID: "run-output-val:step1@1"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Succeed the job with a result pointer — validation will fail
	// because no memory store is configured to fetch the payload.
	err := engine.HandleJobResult(ctx, &pb.JobResult{
		JobId:     "run-output-val:step1@1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "mem://result-quarantined",
	})
	if err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	finalRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	step := finalRun.Steps["step1"]
	if step.Status != StepStatusFailed {
		t.Fatalf("expected step Failed (output validation), got %s", step.Status)
	}
	// The Output field should still contain the ResultPtr for inspection.
	if step.Output != "mem://result-quarantined" {
		t.Fatalf("expected Output to preserve ResultPtr, got %v", step.Output)
	}
}

// ---------------------------------------------------------------------------
// for_each double dispatch prevention
// ---------------------------------------------------------------------------

// TestForEach_ExactlyNDispatches verifies that a for_each with 10 items
// dispatches exactly 10 child jobs, not more.
func TestForEach_ExactlyNDispatches(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-exact-10",
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
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	items := make([]any, 10)
	for i := range items {
		items[i] = fmt.Sprintf("item-%d", i)
	}
	run := &WorkflowRun{
		ID:         "run-exact-10",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      map[string]any{"items": items},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	dispatched := countPublishedSubject(bus, "sys.job.submit")
	if dispatched != 10 {
		t.Fatalf("expected exactly 10 dispatches, got %d", dispatched)
	}
}

// TestForEach_NoDoubleDispatchOnMultiPass verifies that the multi-pass loop
// in scheduleReady does not re-dispatch for_each children that were already
// dispatched in an earlier pass.
func TestForEach_NoDoubleDispatchOnMultiPass(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Two steps: a condition (inline-completes) that depends on nothing,
	// and a for_each that also depends on nothing. Both can dispatch in pass 1.
	// The condition inline-completion triggers pass 2 — for_each must not re-dispatch.
	wf := &Workflow{
		ID:    "wf-multipass-foreach",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"cond": {
				ID:        "cond",
				Type:      StepTypeCondition,
				Condition: "true",
			},
			"fan": {
				ID:      "fan",
				Type:    StepTypeWorker,
				Topic:   "job.default",
				ForEach: "input.items",
			},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-multipass-foreach",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      map[string]any{"items": []any{"a", "b", "c"}},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Exactly 3 for_each children should be dispatched, not 6 (double on pass 2).
	dispatched := countPublishedSubject(bus, "sys.job.submit")
	if dispatched != 3 {
		t.Fatalf("expected exactly 3 dispatches (not doubled), got %d", dispatched)
	}
}
