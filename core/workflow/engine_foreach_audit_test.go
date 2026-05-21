package workflow

import (
	"sync"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// foreachFailSubmitBus fails publishes to SubjectSubmit while letting other
// subjects (cancel, results) through. Used to simulate a transient publish blip
// on the for_each child dispatch path.
type foreachFailSubmitBus struct {
	mu       sync.Mutex
	attempts int
}

func (b *foreachFailSubmitBus) Publish(subject string, _ *pb.BusPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if subject == capsdk.SubjectSubmit {
		b.attempts++
		return errFakePublish
	}
	return nil
}

func (b *foreachFailSubmitBus) Subscribe(string, string, func(*pb.BusPacket) error) error {
	return nil
}

var errFakePublish = newPublishErr()

func newPublishErr() error { return &publishErr{msg: "fake bus publish failure"} }

type publishErr struct{ msg string }

func (e *publishErr) Error() string { return e.msg }

// TestEngineForeachChildDispatchFailureReverts asserts that when publishWithTrace
// fails on a for_each child dispatch, the child step reverts to Pending and
// Attempts is NOT incremented — mirroring the main dispatch path's revert
// behavior at engine.go:2392-2396.
//
// Regression for audit finding #5 (engine.go:2143-2150).
func TestEngineForeachChildDispatchFailureReverts(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &foreachFailSubmitBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-foreach-revert",
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
		ID:         "run-foreach-revert",
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

	// StartRun will fan out two children and try to publish each. Both publishes
	// fail. The current (buggy) behaviour marks the children as StepStatusFailed
	// without ever reverting; the fix mirrors the main path which sets Status=Pending
	// and rolls back Attempts.
	if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final, err := store.GetRun(testCtx(t), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	for _, childID := range []string{"fan[0]", "fan[1]"} {
		child := final.Steps[childID]
		if child == nil {
			t.Fatalf("child %s missing from run", childID)
		}
		if child.Status != StepStatusPending {
			t.Fatalf("child %s status = %s, want StepStatusPending (revert on publish failure)", childID, child.Status)
		}
		if child.Attempts != 0 {
			t.Fatalf("child %s Attempts = %d, want 0 (publish failure must not consume attempt)", childID, child.Attempts)
		}
		if child.JobID != "" {
			t.Fatalf("child %s JobID = %q, want empty after revert", childID, child.JobID)
		}
	}

	// The parent run must NOT be terminally failed when only publish blipped —
	// the children are retryable. (The exact parent status during the
	// retry-pending window is implementation-defined, but it must not be a
	// terminal failure.)
	if final.Status == RunStatusFailed || final.Status == RunStatusDenied {
		t.Fatalf("run terminally %s after transient publish failure; reverted children should not fail the run", final.Status)
	}
}
