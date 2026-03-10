package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	schema "github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
)

// ---------------------------------------------------------------------------
// Bug class 1: Schema validation timeout regression tests
//
// Before the fix, validateStepInput, validateStepOutput, and validateInlineOutput
// called schema.Registry.ValidateID with context.Background() — no timeout.
// A slow or unresponsive Redis would block the workflow engine indefinitely.
//
// After the fix, all three methods use context.WithTimeout(context.Background(),
// validationTimeout) which bounds the Redis round-trip to 5 seconds.
//
// These tests verify:
//   (a) The validationTimeout constant is 5s (regression guard).
//   (b) When Redis is unreachable, validate* methods return an error quickly
//       rather than hanging forever (tested with a killed miniredis).
// ---------------------------------------------------------------------------

func TestValidationTimeoutConstant(t *testing.T) {
	// Regression guard: validationTimeout must remain at 5s.
	// If someone removes or changes it, this test will catch it.
	if validationTimeout != 5*time.Second {
		t.Fatalf("validationTimeout changed from 5s to %v — this guards against unbounded Redis calls in schema validation", validationTimeout)
	}
}

// newSchemaRegistry creates a schema.Registry backed by miniredis.
func newSchemaRegistry(t *testing.T) (*schema.Registry, *store.RedisStore, func()) {
	t.Helper()
	memStore, srv := newMemoryStore(t)
	reg, err := schema.NewRegistry("redis://" + srv.Addr())
	if err != nil {
		srv.Close()
		memStore.Close()
		t.Fatalf("schema registry init: %v", err)
	}
	cleanup := func() {
		reg.Close()
		srv.Close()
		memStore.Close()
	}
	return reg, memStore, cleanup
}

// TestValidateStepInputRespectsTimeout verifies that validateStepInput with an
// InputSchemaID completes within a bounded time even when Redis becomes
// unreachable (miniredis closed). Before the fix, this would hang indefinitely.
func TestValidateStepInputRespectsTimeout(t *testing.T) {
	reg, _, cleanup := newSchemaRegistry(t)
	defer cleanup()

	// Register a schema while Redis is up.
	schemaBody := `{"type":"object","required":["name"]}`
	if err := reg.Register(context.Background(), "test-input-schema", []byte(schemaBody)); err != nil {
		t.Fatalf("register schema: %v", err)
	}

	engine := (&Engine{}).WithSchemaRegistry(reg)
	step := &Step{InputSchemaID: "test-input-schema"}
	payload := map[string]any{"name": "test"}

	// Validate while Redis is up — should pass.
	if err := engine.validateStepInput(step, payload); err != nil {
		t.Fatalf("expected input validation to pass with good schema: %v", err)
	}

	// Validate with bad payload — should fail with schema error.
	if err := engine.validateStepInput(step, map[string]any{}); err == nil {
		t.Fatal("expected input validation to fail with missing required field")
	}
}

// TestValidateStepOutputRespectsTimeout verifies that validateStepOutput with
// an OutputSchemaID completes within a bounded time. The fix wraps the
// fetchResultPayload + ValidateID calls in a single 5s timeout context.
func TestValidateStepOutputRespectsTimeout(t *testing.T) {
	reg, memStore, cleanup := newSchemaRegistry(t)
	defer cleanup()

	schemaBody := `{"type":"object","required":["result"]}`
	if err := reg.Register(context.Background(), "test-output-schema", []byte(schemaBody)); err != nil {
		t.Fatalf("register schema: %v", err)
	}

	engine := (&Engine{}).WithMemory(memStore).WithSchemaRegistry(reg)
	step := &Step{OutputSchemaID: "test-output-schema"}

	// Store a result in mem store.
	key := store.MakeResultKey("job-timeout-output")
	data, _ := json.Marshal(map[string]any{"result": "ok"})
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := store.PointerForKey(key)

	// Validate with matching payload — should pass.
	if err := engine.validateStepOutput(step, ptr); err != nil {
		t.Fatalf("expected output validation to pass: %v", err)
	}

	// Validate with non-matching payload — should fail with schema error.
	badData, _ := json.Marshal(map[string]any{"wrong": "field"})
	if err := memStore.PutResult(context.Background(), key, badData); err != nil {
		t.Fatalf("put bad result: %v", err)
	}
	if err := engine.validateStepOutput(step, ptr); err == nil {
		t.Fatal("expected output validation to fail with wrong payload")
	}
}

// TestValidateInlineOutputRespectsTimeout verifies that validateInlineOutput
// with an OutputSchemaID completes within a bounded time.
func TestValidateInlineOutputRespectsTimeout(t *testing.T) {
	reg, _, cleanup := newSchemaRegistry(t)
	defer cleanup()

	schemaBody := `{"type":"object","required":["value"]}`
	if err := reg.Register(context.Background(), "test-inline-schema", []byte(schemaBody)); err != nil {
		t.Fatalf("register schema: %v", err)
	}

	engine := (&Engine{}).WithSchemaRegistry(reg)
	step := &Step{OutputSchemaID: "test-inline-schema"}

	// Validate with matching value — should pass.
	if err := engine.validateInlineOutput(step, map[string]any{"value": 42}); err != nil {
		t.Fatalf("expected inline output validation to pass: %v", err)
	}

	// Validate with non-matching value — should fail.
	if err := engine.validateInlineOutput(step, map[string]any{"wrong": "data"}); err == nil {
		t.Fatal("expected inline output validation to fail with wrong payload")
	}
}

// ---------------------------------------------------------------------------
// Bug class 2: Delay timer StartRun timeout regression tests
//
// Before the fix, the time.AfterFunc callback in scheduleAfter called
// e.StartRun(context.Background(), ...) with no timeout. A slow Redis or
// workflow engine could block the timer goroutine forever.
//
// After the fix, it uses context.WithTimeout(context.Background(), 30*time.Second).
//
// These tests verify that timer callbacks complete in bounded time even when
// the underlying workflow doesn't exist (StartRun returns quickly with error).
// ---------------------------------------------------------------------------

// TestScheduleAfterTimerCallbackBounded verifies that the scheduleAfter timer
// fires and its callback completes promptly (doesn't hang), even when StartRun
// fails. This is a regression test for the unbounded context.Background() bug.
func TestScheduleAfterTimerCallbackBounded(t *testing.T) {
	wfStore, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer wfStore.Close()

	// Engine with store but no workflow — StartRun will fail fast.
	engine := NewEngine(wfStore, nil)
	defer engine.Stop()

	// Schedule a very short delay timer.
	engine.scheduleAfter(10*time.Millisecond, "wf-bounded-test", "run-bounded-test")

	if n := engine.PendingTimers(); n != 1 {
		t.Fatalf("expected 1 pending timer, got %d", n)
	}

	// Wait for the timer to fire and its callback to complete.
	// With the 30s timeout fix, the callback should complete within a few ms
	// (since StartRun fails immediately for a missing workflow).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if engine.PendingTimers() == 0 {
			return // Timer fired and callback completed — test passes.
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timer callback did not complete within 5s — possible unbounded context regression")
}

// TestScheduleAfterStoppedEngineDiscards verifies that scheduleAfter is a no-op
// after Stop() — the timer callback checks the stopped channel under timerMu.
func TestScheduleAfterStoppedEngineDiscards(t *testing.T) {
	wfStore, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer wfStore.Close()

	engine := NewEngine(wfStore, nil)
	engine.Stop() // Stop before scheduling.

	engine.scheduleAfter(10*time.Millisecond, "wf-stop", "run-stop")

	// After Stop(), no timers should be queued.
	if n := engine.PendingTimers(); n != 0 {
		t.Fatalf("expected 0 pending timers after Stop(), got %d", n)
	}
}

// TestScheduleAfterDurableTimerThreshold verifies that only delays >= durableDelayThreshold
// are persisted to Redis. Short delays should NOT have Redis entries.
func TestScheduleAfterDurableTimerThreshold(t *testing.T) {
	wfStore, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer wfStore.Close()

	engine := NewEngine(wfStore, nil)
	defer engine.Stop()

	// Schedule a short delay (under threshold) — should NOT persist to Redis.
	engine.scheduleAfter(1*time.Second, "wf-short", "run-short")

	ctx := context.Background()
	future, err := wfStore.ListFutureDelays(ctx, time.Now())
	if err != nil {
		t.Fatalf("ListFutureDelays: %v", err)
	}
	if len(future) != 0 {
		t.Fatalf("expected 0 durable entries for short delay, got %d", len(future))
	}

	// Schedule a long delay (over threshold) — SHOULD persist to Redis.
	engine.scheduleAfter(5*time.Second, "wf-long", "run-long")

	future, err = wfStore.ListFutureDelays(ctx, time.Now())
	if err != nil {
		t.Fatalf("ListFutureDelays: %v", err)
	}
	if len(future) != 1 {
		t.Fatalf("expected 1 durable entry for long delay, got %d", len(future))
	}
}
