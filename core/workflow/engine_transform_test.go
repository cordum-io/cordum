package workflow

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TestTransformBasic: template expressions are evaluated and stored as output
// ---------------------------------------------------------------------------

func TestTransformBasic(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-transform-basic",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"xform": {
				ID:   "xform",
				Type: StepTypeTransform,
				Input: map[string]any{
					"greeting": "hello ${ input.name }",
					"doubled":  "${ input.count }} x2",
				},
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-transform-basic",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{"name": "world", "count": 42},
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final := mustGetRun(t, store, run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	sr := final.Steps["xform"]
	if sr == nil || sr.Status != StepStatusSucceeded {
		t.Fatalf("expected xform step succeeded, got %#v", sr)
	}
	output, ok := sr.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected output map, got %T", sr.Output)
	}
	greeting, _ := output["greeting"].(string)
	if !strings.Contains(greeting, "world") {
		t.Fatalf("expected greeting to contain 'world', got %q", greeting)
	}
}

// ---------------------------------------------------------------------------
// TestTransformBadExpression: malformed expression fails with error
// ---------------------------------------------------------------------------

func TestTransformBadExpression(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-transform-bad-expr",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"xform": {
				ID:   "xform",
				Type: StepTypeTransform,
				Input: map[string]any{
					"value": "${ }",
				},
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-transform-bad-expr",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{},
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final := mustGetRun(t, store, run.ID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	sr := final.Steps["xform"]
	if sr == nil || sr.Status != StepStatusFailed {
		t.Fatalf("expected xform step failed, got %#v", sr)
	}
	msg, _ := sr.Error["message"].(string)
	if msg == "" {
		t.Fatalf("expected error message, got empty")
	}
	if !strings.Contains(msg, "transform expression error") {
		t.Fatalf("expected transform expression error prefix, got %q", msg)
	}
}

// ---------------------------------------------------------------------------
// TestTransformOutputPath: result written to run context via OutputPath
// ---------------------------------------------------------------------------

func TestTransformOutputPath(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-transform-outpath",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"xform": {
				ID:   "xform",
				Type: StepTypeTransform,
				Input: map[string]any{
					"total": "${ input.a }",
				},
				OutputPath: "ctx.summary",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-transform-outpath",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{"a": 100},
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final := mustGetRun(t, store, run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	// Check output_path: ctx.summary should exist in run context
	ctxMap, ok := final.Context["ctx"].(map[string]any)
	if !ok {
		t.Fatalf("expected ctx map in run context, got %#v", final.Context["ctx"])
	}
	summary, ok := ctxMap["summary"].(map[string]any)
	if !ok {
		t.Fatalf("expected summary map, got %#v", ctxMap["summary"])
	}
	if summary["total"] == nil {
		t.Fatalf("expected total key in summary, got %#v", summary)
	}
}

// ---------------------------------------------------------------------------
// TestTransformEmptyInput: nil input succeeds with empty output map
// ---------------------------------------------------------------------------

func TestTransformEmptyInput(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-transform-empty",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"xform": {
				ID:   "xform",
				Type: StepTypeTransform,
				// Input deliberately nil
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-transform-empty",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{},
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final := mustGetRun(t, store, run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	sr := final.Steps["xform"]
	if sr == nil || sr.Status != StepStatusSucceeded {
		t.Fatalf("expected xform step succeeded, got %#v", sr)
	}
	output, ok := sr.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected empty output map, got %T", sr.Output)
	}
	if len(output) != 0 {
		t.Fatalf("expected 0 keys in output, got %d", len(output))
	}
}

// ---------------------------------------------------------------------------
// TestTransformPreservesTypes: non-string values stay as their original type
// ---------------------------------------------------------------------------

func TestTransformPreservesTypes(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-transform-types",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"xform": {
				ID:   "xform",
				Type: StepTypeTransform,
				Input: map[string]any{
					"num":  "${ input.count }",
					"flag": "${ input.active }",
					"raw":  "static-value",
				},
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-transform-types",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{"count": 42, "active": true},
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final := mustGetRun(t, store, run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	sr := final.Steps["xform"]
	output, ok := sr.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected output map, got %T", sr.Output)
	}
	// raw string with no template delimiters should stay as-is
	if raw, _ := output["raw"].(string); raw != "static-value" {
		t.Fatalf("expected raw 'static-value', got %#v", output["raw"])
	}
}

// ---------------------------------------------------------------------------
// TestTransformNestedExpressions: nested maps are fully evaluated
// ---------------------------------------------------------------------------

func TestTransformNestedExpressions(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-transform-nested",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"xform": {
				ID:   "xform",
				Type: StepTypeTransform,
				Input: map[string]any{
					"outer": map[string]any{
						"inner_val": "${ input.msg }",
					},
					"flat": "${ input.msg }",
				},
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-transform-nested",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{"msg": "deep"},
		Steps:      map[string]*StepRun{},
		Status:     RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final := mustGetRun(t, store, run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	sr := final.Steps["xform"]
	output, ok := sr.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected output map, got %T", sr.Output)
	}
	outerMap, ok := output["outer"].(map[string]any)
	if !ok {
		t.Fatalf("expected outer to be a map, got %T", output["outer"])
	}
	innerVal, _ := outerMap["inner_val"].(string)
	if !strings.Contains(innerVal, "deep") {
		t.Fatalf("expected inner_val to contain 'deep', got %q", innerVal)
	}
	flatVal, _ := output["flat"].(string)
	if !strings.Contains(flatVal, "deep") {
		t.Fatalf("expected flat to contain 'deep', got %q", flatVal)
	}
}
