package workflow

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func setupStorageRun(
	t *testing.T,
	workflowID string,
	runID string,
	runInput map[string]any,
	steps map[string]*Step,
) (*RedisStore, *Engine, *recordingBus, string) {
	t.Helper()

	store := newWorkflowStore(t)
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    workflowID,
		OrgID: "org-1",
		Steps: steps,
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         runID,
		WorkflowID: workflowID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      runInput,
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Advance until the run reaches a terminal state or we exhaust retries.
	// Needed because Go map iteration is non-deterministic — dependent steps
	// may be checked before their prerequisites in a single scheduleReady pass.
	for range 10 {
		if err := engine.StartRun(context.Background(), workflowID, runID); err != nil {
			t.Fatalf("start run: %v", err)
		}
		r := mustGetRun(t, store, runID)
		if r.Status != RunStatusRunning && r.Status != RunStatusPending {
			break
		}
	}
	return store, engine, bus, runID
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestStorageWriteReadContext(t *testing.T) {
	store, _, _, runID := setupStorageRun(
		t,
		"wf-storage-wr",
		"run-storage-wr",
		map[string]any{"greeting": "hello"},
		map[string]*Step{
			"write": {
				ID:   "write",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "write",
					"key":       "data.message",
					"value":     "hello world",
				},
			},
			"read": {
				ID:   "read",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "read",
					"key":       "data.message",
				},
				DependsOn: []string{"write"},
			},
		},
	)
	defer store.Close()

	run := mustGetRun(t, store, runID)
	if run.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", run.Status)
	}

	writeSR := run.Steps["write"]
	if writeSR == nil || writeSR.Status != StepStatusSucceeded {
		t.Fatalf("expected write step succeeded, got %#v", writeSR)
	}
	writeOut, _ := writeSR.Output.(map[string]any)
	if writeOut["operation"] != "write" || writeOut["key"] != "data.message" {
		t.Fatalf("unexpected write output: %#v", writeOut)
	}

	readSR := run.Steps["read"]
	if readSR == nil || readSR.Status != StepStatusSucceeded {
		t.Fatalf("expected read step succeeded, got %#v", readSR)
	}
	readOut, _ := readSR.Output.(map[string]any)
	if readOut["value"] != "hello world" {
		t.Fatalf("expected read value 'hello world', got %#v", readOut["value"])
	}
}

func TestStorageDeleteContext(t *testing.T) {
	store, engine, _, runID := setupStorageRun(
		t,
		"wf-storage-del",
		"run-storage-del",
		nil,
		map[string]*Step{
			"write": {
				ID:   "write",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "write",
					"key":       "temp.value",
					"value":     42,
				},
			},
			"delete": {
				ID:   "delete",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "delete",
					"key":       "temp.value",
				},
				DependsOn: []string{"write"},
			},
			"verify": {
				ID:   "verify",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "read",
					"key":       "temp.value",
				},
				DependsOn: []string{"delete"},
			},
		},
	)
	defer store.Close()

	// After write+delete, the verify read should fail because key was deleted.
	run := mustGetRun(t, store, runID)

	// The run might need another advance if steps haven't all resolved.
	if run.Status == RunStatusRunning {
		_ = engine.StartRun(context.Background(), run.WorkflowID, runID)
		run = mustGetRun(t, store, runID)
	}

	writeSR := run.Steps["write"]
	if writeSR == nil || writeSR.Status != StepStatusSucceeded {
		t.Fatalf("expected write step succeeded, got %#v", writeSR)
	}

	deleteSR := run.Steps["delete"]
	if deleteSR == nil || deleteSR.Status != StepStatusSucceeded {
		t.Fatalf("expected delete step succeeded, got %#v", deleteSR)
	}

	verifySR := run.Steps["verify"]
	if verifySR == nil || verifySR.Status != StepStatusFailed {
		t.Fatalf("expected verify step failed (key deleted), got %#v", verifySR)
	}
	errMsg, _ := verifySR.Error["message"].(string)
	if errMsg == "" {
		t.Fatal("expected error message for missing key")
	}
}

func TestStorageReadMissing(t *testing.T) {
	store, _, _, runID := setupStorageRun(
		t,
		"wf-storage-miss",
		"run-storage-miss",
		nil,
		map[string]*Step{
			"read": {
				ID:   "read",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "read",
					"key":       "nonexistent.key",
				},
			},
		},
	)
	defer store.Close()

	run := mustGetRun(t, store, runID)
	// Run should fail because the read step fails.
	if run.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", run.Status)
	}

	readSR := run.Steps["read"]
	if readSR == nil || readSR.Status != StepStatusFailed {
		t.Fatalf("expected read step failed, got %#v", readSR)
	}
	errMsg, _ := readSR.Error["message"].(string)
	if errMsg == "" {
		t.Fatal("expected error message for missing key")
	}
}

func TestStorageNestedPath(t *testing.T) {
	store, _, _, runID := setupStorageRun(
		t,
		"wf-storage-nested",
		"run-storage-nested",
		nil,
		map[string]*Step{
			"write": {
				ID:   "write",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "write",
					"key":       "user.prefs.theme",
					"value":     "dark",
				},
			},
			"read": {
				ID:   "read",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "read",
					"key":       "user.prefs.theme",
				},
				DependsOn:  []string{"write"},
				OutputPath: "theme_result",
			},
		},
	)
	defer store.Close()

	run := mustGetRun(t, store, runID)
	if run.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", run.Status)
	}

	readSR := run.Steps["read"]
	if readSR == nil || readSR.Status != StepStatusSucceeded {
		t.Fatalf("expected read step succeeded, got %#v", readSR)
	}
	readOut, _ := readSR.Output.(map[string]any)
	if readOut["value"] != "dark" {
		t.Fatalf("expected theme 'dark', got %#v", readOut["value"])
	}

	// Check OutputPath wrote to run context
	themeResult, found := getContextPath(run.Context, "theme_result")
	if !found {
		t.Fatal("expected theme_result in run context via OutputPath")
	}
	resultMap, ok := themeResult.(map[string]any)
	if !ok {
		t.Fatalf("expected theme_result to be map, got %T", themeResult)
	}
	if resultMap["value"] != "dark" {
		t.Fatalf("expected theme_result.value 'dark', got %#v", resultMap["value"])
	}
}

func TestStorageExpressionValue(t *testing.T) {
	store, _, _, runID := setupStorageRun(
		t,
		"wf-storage-expr",
		"run-storage-expr",
		map[string]any{"name": "Alice"},
		map[string]*Step{
			"write": {
				ID:   "write",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "write",
					"key":       "greeting",
					"value":     "${input.name}",
				},
			},
			"read": {
				ID:   "read",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "read",
					"key":       "greeting",
				},
				DependsOn: []string{"write"},
			},
		},
	)
	defer store.Close()

	run := mustGetRun(t, store, runID)
	if run.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", run.Status)
	}

	readSR := run.Steps["read"]
	if readSR == nil || readSR.Status != StepStatusSucceeded {
		t.Fatalf("expected read step succeeded, got %#v", readSR)
	}
	readOut, _ := readSR.Output.(map[string]any)
	if readOut["value"] != "Alice" {
		t.Fatalf("expected evaluated expression 'Alice', got %#v", readOut["value"])
	}
}

func TestStorageUnknownOperation(t *testing.T) {
	store, _, _, runID := setupStorageRun(
		t,
		"wf-storage-unknown",
		"run-storage-unknown",
		nil,
		map[string]*Step{
			"bad": {
				ID:   "bad",
				Type: StepTypeStorage,
				Input: map[string]any{
					"operation": "upsert",
					"key":       "foo",
				},
			},
		},
	)
	defer store.Close()

	run := mustGetRun(t, store, runID)
	if run.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", run.Status)
	}

	badSR := run.Steps["bad"]
	if badSR == nil || badSR.Status != StepStatusFailed {
		t.Fatalf("expected bad step failed, got %#v", badSR)
	}
	errMsg, _ := badSR.Error["message"].(string)
	if errMsg == "" {
		t.Fatal("expected error message for unknown operation")
	}
}

// ---------------------------------------------------------------------------
// Context path helper tests
// ---------------------------------------------------------------------------

func TestGetContextPath(t *testing.T) {
	ctx := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": 42,
			},
		},
		"flat": "value",
	}

	if val, ok := getContextPath(ctx, "a.b.c"); !ok || val != 42 {
		t.Fatalf("expected 42, got %v (found=%v)", val, ok)
	}
	if val, ok := getContextPath(ctx, "flat"); !ok || val != "value" {
		t.Fatalf("expected 'value', got %v (found=%v)", val, ok)
	}
	if _, ok := getContextPath(ctx, "missing"); ok {
		t.Fatal("expected missing key to return false")
	}
	if _, ok := getContextPath(ctx, "a.b.missing"); ok {
		t.Fatal("expected nested missing key to return false")
	}
	if _, ok := getContextPath(nil, "a"); ok {
		t.Fatal("expected nil context to return false")
	}
}

func TestDeleteContextPath(t *testing.T) {
	ctx := map[string]any{
		"a": map[string]any{
			"b": "keep",
			"c": "remove",
		},
		"top": "remove",
	}

	deleteContextPath(ctx, "a.c")
	if _, ok := getContextPath(ctx, "a.c"); ok {
		t.Fatal("expected a.c to be deleted")
	}
	if val, ok := getContextPath(ctx, "a.b"); !ok || val != "keep" {
		t.Fatal("expected a.b to still exist")
	}

	deleteContextPath(ctx, "top")
	if _, ok := getContextPath(ctx, "top"); ok {
		t.Fatal("expected top to be deleted")
	}

	// Deleting non-existent path should not panic.
	deleteContextPath(ctx, "missing.path")
	deleteContextPath(nil, "anything")
}
