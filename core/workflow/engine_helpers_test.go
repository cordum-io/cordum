package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// testCtx returns a context with a deadline derived from the test timeout.
// Falls back to 30s if no deadline is set. Prevents tests from hanging forever.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	deadline, ok := t.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	t.Cleanup(cancel)
	return ctx
}

type stubConfig struct {
	cfg map[string]any
}

func (s stubConfig) Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error) {
	return s.cfg, nil
}

func newMemoryStore(t *testing.T) (*store.RedisStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	rs, err := store.NewRedisStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("memory store init: %v", err)
	}
	return rs, srv
}

func TestEvalTemplateString(t *testing.T) {
	scope := map[string]any{"input": map[string]any{"name": "core"}}
	val, err := evalTemplateString("${input.name}", scope)
	if err != nil || val != "core" {
		t.Fatalf("expected evaluated template, got %v err=%v", val, err)
	}
	val, err = evalTemplateString("hello ${input.name}", scope)
	if err != nil || val != "hello core" {
		t.Fatalf("expected interpolated template, got %v err=%v", val, err)
	}
	if _, err := evalTemplateString("${input.name", scope); err == nil {
		t.Fatalf("expected unterminated template error")
	}
}

func TestBuildJobPayloadAndValidation(t *testing.T) {
	engine := &Engine{}
	run := &WorkflowRun{Input: map[string]any{"name": "core"}}
	step := &Step{Input: map[string]any{"msg": "${input.name}"}}
	payload, err := engine.buildJobPayload(run, step, "item-1")
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload["msg"] != "core" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload["item"] != "item-1" {
		t.Fatalf("expected item field")
	}

	schemaStep := &Step{InputSchema: map[string]any{"type": "object", "required": []any{"msg"}}}
	if err := engine.validateStepInput(schemaStep, map[string]any{"msg": "ok"}); err != nil {
		t.Fatalf("validate input: %v", err)
	}
	if err := engine.validateStepInput(schemaStep, map[string]any{}); err == nil {
		t.Fatalf("expected input schema error")
	}
}

// TestBuildJobPayloadStripsNilValues verifies that nil-valued keys produced
// by unresolved optional template expressions are stripped before schema
// validation. Without this, a workflow input like `sources: ${input.missing}`
// would inject "sources": null into the payload, causing JSON Schema to reject
// null against "type": "array" even though the field is not required.
func TestBuildJobPayloadStripsNilValues(t *testing.T) {
	engine := &Engine{}
	run := &WorkflowRun{Input: map[string]any{
		"date_range": map[string]any{"start": "2026-03-01", "end": "2026-03-14"},
	}}
	// Step references ${input.optional_field} which doesn't exist in run input.
	step := &Step{
		Input: map[string]any{
			"required_field": "${input.date_range}",
			"optional_field": "${input.missing_field}",
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"required_field": map[string]any{"type": "object"},
				"optional_field": map[string]any{"type": "array"},
			},
			"required": []any{"required_field"},
		},
	}
	payload, err := engine.buildJobPayload(run, step, nil)
	if err != nil {
		t.Fatalf("buildJobPayload should succeed when optional field resolves to nil: %v", err)
	}
	if payload["required_field"] == nil {
		t.Fatal("required_field should be present")
	}
	if _, exists := payload["optional_field"]; exists {
		t.Fatal("nil-valued optional_field should have been stripped from payload")
	}
}

// TestBuildJobPayloadNilRunInputReturnsExplicitError verifies that when
// run.Input is nil and a step template references ${input.date_range},
// the error message explicitly mentions the field name and "resolved to nil"
// instead of a confusing "missing properties" schema validation error.
func TestBuildJobPayloadNilRunInputReturnsExplicitError(t *testing.T) {
	engine := &Engine{}
	run := &WorkflowRun{Input: nil}
	step := &Step{
		Input: map[string]any{
			"scan_type":  "product",
			"date_range": "${input.date_range}",
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scan_type":  map[string]any{"type": "string"},
				"date_range": map[string]any{"type": "object"},
			},
			"required": []any{"scan_type", "date_range"},
		},
	}
	_, err := engine.buildJobPayload(run, step, nil)
	if err == nil {
		t.Fatal("expected error when required template resolves to nil")
	}
	if !strings.Contains(err.Error(), "date_range") {
		t.Fatalf("error should mention the field name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "resolved to nil") {
		t.Fatalf("error should say 'resolved to nil', got: %v", err)
	}
}

// TestBuildJobPayloadPartialRunInputReturnsExplicitError verifies that when
// run.Input exists but is missing a key referenced by a required step
// template, the error is explicit about which field resolved to nil.
func TestBuildJobPayloadPartialRunInputReturnsExplicitError(t *testing.T) {
	engine := &Engine{}
	run := &WorkflowRun{Input: map[string]any{"scan_type": "daily"}}
	step := &Step{
		Input: map[string]any{
			"scan_type":  "product",
			"date_range": "${input.date_range}",
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scan_type":  map[string]any{"type": "string"},
				"date_range": map[string]any{"type": "object"},
			},
			"required": []any{"scan_type", "date_range"},
		},
	}
	_, err := engine.buildJobPayload(run, step, nil)
	if err == nil {
		t.Fatal("expected error when required template resolves to nil")
	}
	if !strings.Contains(err.Error(), "date_range") {
		t.Fatalf("error should mention the field name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "resolved to nil") {
		t.Fatalf("error should say 'resolved to nil', got: %v", err)
	}
}

func TestValidateStepOutputInlineSchema(t *testing.T) {
	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	engine := (&Engine{}).WithMemory(memStore)
	step := &Step{OutputSchema: map[string]any{"type": "object", "required": []any{"result"}}}

	key := store.MakeResultKey("job-1")
	payload := map[string]any{"result": "ok"}
	data, _ := json.Marshal(payload)
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := store.PointerForKey(key)
	if err := engine.validateStepOutput(step, ptr); err != nil {
		t.Fatalf("validate output: %v", err)
	}

	badPayload := map[string]any{"nope": "bad"}
	data, _ = json.Marshal(badPayload)
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put bad result: %v", err)
	}
	if err := engine.validateStepOutput(step, ptr); err == nil {
		t.Fatalf("expected output schema error")
	}
}

// TestValidateStepOutputFailClosedOnFetchError verifies that output schema
// validation returns an error (fail-closed) when the result payload cannot
// be fetched, rather than silently skipping validation.
func TestValidateStepOutputFailClosedOnFetchError(t *testing.T) {
	// Engine with nil mem store — fetchResultPayload will return (nil, false).
	engine := &Engine{}
	step := &Step{OutputSchema: map[string]any{"type": "object", "required": []any{"result"}}}

	err := engine.validateStepOutput(step, "mem://some-key")
	if err == nil {
		t.Fatal("expected fail-closed error when result payload cannot be fetched, got nil")
	}
	if !strings.Contains(err.Error(), "unable to fetch result payload") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestValidateStepOutputFailClosedOnMissingResult verifies fail-closed when
// the result key exists in the pointer but has no data in the store.
func TestValidateStepOutputFailClosedOnMissingResult(t *testing.T) {
	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	engine := (&Engine{}).WithMemory(memStore)
	step := &Step{OutputSchema: map[string]any{"type": "object", "required": []any{"result"}}}

	// Use a valid pointer format but don't put any data in the store.
	key := store.MakeResultKey("job-missing")
	ptr := store.PointerForKey(key)

	err := engine.validateStepOutput(step, ptr)
	if err == nil {
		t.Fatal("expected fail-closed error when result is missing from store, got nil")
	}
	if !strings.Contains(err.Error(), "unable to fetch result payload") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestValidateStepOutputNoSchemaSkipsValidation verifies that when no output
// schema is configured, validation is skipped regardless of fetch success.
func TestValidateStepOutputNoSchemaSkipsValidation(t *testing.T) {
	engine := &Engine{} // nil mem
	step := &Step{}     // no OutputSchema, no OutputSchemaID

	// Should return nil — no schema means no validation needed.
	if err := engine.validateStepOutput(step, "mem://some-key"); err != nil {
		t.Fatalf("expected nil when no schema configured, got: %v", err)
	}
}

// TestValidateStepOutputSchemaRegistryUnavailable verifies that validation
// fails when OutputSchemaID is set but the schema registry is nil.
func TestValidateStepOutputSchemaRegistryUnavailable(t *testing.T) {
	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	engine := (&Engine{}).WithMemory(memStore) // no schema registry
	step := &Step{OutputSchemaID: "schema-123"}

	// Put valid data so fetchResultPayload succeeds — the error should come
	// from the nil schema registry, not from fetch.
	key := store.MakeResultKey("job-registry")
	data, _ := json.Marshal(map[string]any{"result": "ok"})
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := store.PointerForKey(key)

	err := engine.validateStepOutput(step, ptr)
	if err == nil {
		t.Fatal("expected error when schema registry is nil")
	}
	if !strings.Contains(err.Error(), "schema registry unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildJobRequestMetadata(t *testing.T) {
	engine := (&Engine{}).WithConfig(stubConfig{cfg: map[string]any{"limit": 1}})
	wf := &Workflow{ID: "wf1"}
	run := &WorkflowRun{ID: "run1", OrgID: "org", TeamID: "team", Input: map[string]any{"priority": "critical", "memory_id": "mem1"}, Metadata: map[string]string{"dry_run": "true"}}
	step := &Step{ID: "step", Type: StepTypeWorker, WorkerID: "worker", Topic: "job.test", TimeoutSec: 5, RouteLabels: map[string]string{"pool": "default"}, Meta: &StepMeta{ActorId: "actor", ActorType: "human", PackId: "pack", Capability: "cap", RiskTags: []string{"write"}}}

	req := engine.buildJobRequest(context.Background(), wf, run, step, "step", "job-1")
	if req.Priority != pb.JobPriority_JOB_PRIORITY_CRITICAL {
		t.Fatalf("expected critical priority")
	}
	if req.MemoryId != "mem1" {
		t.Fatalf("expected memory id")
	}
	if req.Labels["worker_id"] != "worker" || req.Labels["pool"] != "default" {
		t.Fatalf("expected labels")
	}
	if req.Env["dry_run"] != "true" || req.Labels["dry_run"] != "true" {
		t.Fatalf("expected dry_run flags")
	}
	if req.Budget == nil || req.Budget.DeadlineMs != 5000 {
		t.Fatalf("expected timeout budget")
	}
	if req.Meta == nil || req.Meta.GetActorId() != "actor" || req.Meta.GetPackId() != "pack" {
		t.Fatalf("expected metadata")
	}
	if req.Env["CORDUM_EFFECTIVE_CONFIG"] == "" {
		t.Fatalf("expected effective config in env")
	}
	if !strings.Contains(req.Env["CORDUM_EFFECTIVE_CONFIG"], "limit") {
		t.Fatalf("unexpected effective config payload")
	}
}

func TestHelperFunctions(t *testing.T) {
	if actorTypeFromString("human") != pb.ActorType_ACTOR_TYPE_HUMAN {
		t.Fatalf("expected human actor type")
	}
	if actorTypeFromString("service") != pb.ActorType_ACTOR_TYPE_SERVICE {
		t.Fatalf("expected service actor type")
	}
	if actorTypeFromString("other") != pb.ActorType_ACTOR_TYPE_UNSPECIFIED {
		t.Fatalf("expected unspecified actor type")
	}

	cleaned := cleanStrings([]string{"", "a", " b "})
	if len(cleaned) != 2 || cleaned[1] != "b" {
		t.Fatalf("unexpected cleaned strings: %#v", cleaned)
	}

	meta := &pb.JobMetadata{TenantId: "tenant"}
	if metaEmpty(meta) {
		t.Fatalf("expected non-empty meta")
	}

	deps := map[string]struct{}{}
	wf := &Workflow{Steps: map[string]*Step{"a": {ID: "a", DependsOn: []string{"b"}}, "b": {ID: "b"}}}
	collectDependencies(wf, "a", deps)
	if _, ok := deps["b"]; !ok {
		t.Fatalf("expected dependency collected")
	}

	input := map[string]any{"k": map[string]any{"v": 1}}
	clone := cloneMap(input)
	if clone["k"].(map[string]any)["v"].(int) != 1 {
		t.Fatalf("unexpected clone")
	}
}

func TestSplitJobID(t *testing.T) {
	run, step := splitJobID("run:step@3")
	if run != "run" || step != "step" {
		t.Fatalf("unexpected split: %s %s", run, step)
	}
	run, step = splitJobID("run:with:step@2")
	if run != "run" || step != "with:step" {
		t.Fatalf("unexpected split for multi: %s %s", run, step)
	}
	run, step = splitJobID("bad")
	if run != "" || step != "" {
		t.Fatalf("expected empty split")
	}
}

func TestSetContextPathAndRecordStepOutput(t *testing.T) {
	run := &WorkflowRun{Context: map[string]any{}}
	if err := setContextPath(run.Context, "outputs.value", "ok"); err != nil {
		t.Fatalf("set context path: %v", err)
	}
	if run.Context["outputs"].(map[string]any)["value"] != "ok" {
		t.Fatalf("expected nested context value")
	}
	if err := setContextPath(run.Context, "outputs..bad", "no"); err == nil {
		t.Fatalf("expected invalid context path error")
	}

	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	jobID := "job-ctx"
	key := store.MakeResultKey(jobID)
	data, _ := json.Marshal(map[string]any{"result": "ok"})
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := store.PointerForKey(key)
	step := &Step{OutputPath: "outputs.result"}
	recordStepOutput(context.Background(), memStore, run, "step", step, ptr, true)
	steps := run.Context["steps"].(map[string]any)
	entry := steps["step"].(map[string]any)
	if entry["output"].(map[string]any)["result"] != "ok" {
		t.Fatalf("expected inline output")
	}
	if run.Context["outputs"].(map[string]any)["result"].(map[string]any)["result"] != "ok" {
		t.Fatalf("expected output path set")
	}

	recordStepInlineOutput(run, "inline", nil, map[string]any{"x": 1})
	inlineEntry := run.Context["steps"].(map[string]any)["inline"].(map[string]any)
	if got := inlineEntry["output"].(map[string]any)["x"]; got != 1 {
		t.Fatalf("expected inline output stored")
	}
}

func TestInlineResultAndFetchPayload(t *testing.T) {
	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	jobID := "job-inline"
	key := store.MakeResultKey(jobID)
	data, _ := json.Marshal(map[string]any{"result": "ok"})
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := store.PointerForKey(key)
	if val, ok := inlineResult(context.Background(), memStore, ptr); !ok || val.(map[string]any)["result"] != "ok" {
		t.Fatalf("expected inline result")
	}
	if val, ok := fetchResultPayload(context.Background(), memStore, ptr); !ok || val.(map[string]any)["result"] != "ok" {
		t.Fatalf("expected fetched payload")
	}

	large := make([]byte, maxInlineResultBytes+1)
	if err := memStore.PutResult(context.Background(), key, large); err != nil {
		t.Fatalf("put large result: %v", err)
	}
	if _, ok := inlineResult(context.Background(), memStore, ptr); ok {
		t.Fatalf("expected inline result to be skipped for large payload")
	}
}

func TestEvalForEachAndBuildJobRequestDefaults(t *testing.T) {
	scope := map[string]any{"input": map[string]any{"items": []any{"a", "b"}}}
	items, err := evalForEach("input.items", scope)
	if err != nil || len(items) != 2 {
		t.Fatalf("expected for_each items, got %v err=%v", items, err)
	}

	engine := &Engine{}
	wf := &Workflow{ID: "wf-default"}
	run := &WorkflowRun{ID: "run-default", OrgID: "org", TeamID: "team", Input: map[string]any{}}
	step := &Step{ID: "step", Type: StepTypeWorker, Topic: "job.test"}
	req := engine.buildJobRequest(context.Background(), wf, run, step, "step", "job-1")
	if req.MemoryId != "run:"+run.ID {
		t.Fatalf("expected default memory id")
	}
	if req.Env["context_mode"] != "raw" {
		t.Fatalf("expected default context mode")
	}
}

// ---------------------------------------------------------------------------
// extractDataPath unit tests
// ---------------------------------------------------------------------------

func TestExtractDataPath(t *testing.T) {
	cases := []struct {
		name   string
		value  any
		path   string
		wantOK bool
	}{
		{"empty path returns original", map[string]any{"k": "v"}, "", true},
		{"single level", map[string]any{"data": map[string]any{"x": 1}}, "data", true},
		{"nested path", map[string]any{"data": map[string]any{"nested": "val"}}, "data.nested", true},
		{"missing path", map[string]any{"data": "ok"}, "missing", false},
		{"deep missing", map[string]any{"a": map[string]any{"b": "c"}}, "a.z", false},
		{"non-map value", "string-value", "data", false},
		{"nil value", nil, "data", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := extractDataPath(tc.value, tc.path)
			if ok != tc.wantOK {
				t.Fatalf("extractDataPath ok=%v, want %v", ok, tc.wantOK)
			}
		})
	}

	// Verify specific extracted values.
	val, ok := extractDataPath(map[string]any{"data": map[string]any{"nested": "val"}}, "data.nested")
	if !ok || val != "val" {
		t.Fatalf("expected 'val', got %v (ok=%v)", val, ok)
	}
}

// ---------------------------------------------------------------------------
// validateStepOutput with ResultDataPath
// ---------------------------------------------------------------------------

func TestValidateStepOutputWithResultDataPath(t *testing.T) {
	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	engine := (&Engine{}).WithMemory(memStore)

	// Worker returns a wrapper: {action: "draft", status: "ok", data: {drafts: [...]}}
	// Schema validates the inner data field only.
	wrapper := map[string]any{
		"action": "draft",
		"status": "ok",
		"data":   map[string]any{"drafts": []any{"draft-1", "draft-2"}},
	}
	key := store.MakeResultKey("job-rdp-1")
	data, _ := json.Marshal(wrapper)
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := store.PointerForKey(key)

	// Schema requires "drafts" field — only valid for the extracted data, not the wrapper.
	step := &Step{
		ResultDataPath: "data",
		OutputSchema:   map[string]any{"type": "object", "required": []any{"drafts"}},
	}
	if err := engine.validateStepOutput(step, ptr); err != nil {
		t.Fatalf("expected validation to pass with result_data_path, got: %v", err)
	}

	// Without ResultDataPath, the same schema should FAIL (wrapper doesn't have "drafts").
	stepNoRDP := &Step{
		OutputSchema: map[string]any{"type": "object", "required": []any{"drafts"}},
	}
	if err := engine.validateStepOutput(stepNoRDP, ptr); err == nil {
		t.Fatal("expected validation to fail without result_data_path")
	}
}

func TestValidateStepOutputResultDataPathMissing(t *testing.T) {
	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	engine := (&Engine{}).WithMemory(memStore)

	wrapper := map[string]any{"action": "draft", "status": "ok"}
	key := store.MakeResultKey("job-rdp-missing")
	data, _ := json.Marshal(wrapper)
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := store.PointerForKey(key)

	step := &Step{
		ResultDataPath: "nonexistent",
		OutputSchema:   map[string]any{"type": "object"},
	}
	err := engine.validateStepOutput(step, ptr)
	if err == nil {
		t.Fatal("expected error for missing result_data_path")
	}
	if !strings.Contains(err.Error(), "result_data_path") {
		t.Fatalf("expected result_data_path error message, got: %v", err)
	}
}

func TestRecordStepOutputWithResultDataPath(t *testing.T) {
	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer func() { _ = memStore.Close() }()

	wrapper := map[string]any{
		"action": "draft",
		"status": "ok",
		"data":   map[string]any{"drafts": []any{"d1"}},
	}
	key := store.MakeResultKey("job-rdp-record")
	data, _ := json.Marshal(wrapper)
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := store.PointerForKey(key)

	run := &WorkflowRun{ID: "run-rdp", Context: map[string]any{}}
	step := &Step{ResultDataPath: "data", OutputPath: "ctx.outreach"}

	recordStepOutput(context.Background(), memStore, run, "step-1", step, ptr, true)

	// Verify the output stored is the extracted data, not the wrapper.
	steps := run.Context["steps"].(map[string]any)
	entry := steps["step-1"].(map[string]any)
	output, ok := entry["output"].(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T: %v", entry["output"], entry["output"])
	}
	if _, hasDrafts := output["drafts"]; !hasDrafts {
		t.Fatalf("expected extracted data with drafts field, got: %v", output)
	}
	if _, hasAction := output["action"]; hasAction {
		t.Fatal("extracted data should NOT contain wrapper field 'action'")
	}

	// Verify output_path stores extracted data.
	ctxVal, found := getContextPath(run.Context, "ctx.outreach")
	if !found {
		t.Fatal("expected ctx.outreach to be set")
	}
	outreachMap, ok := ctxVal.(map[string]any)
	if !ok {
		t.Fatalf("expected map at ctx.outreach, got %T", ctxVal)
	}
	if _, hasDrafts := outreachMap["drafts"]; !hasDrafts {
		t.Fatal("expected ctx.outreach to contain extracted drafts")
	}
}

func TestSkipDependentSteps_Direct(t *testing.T) {
	wfDef := &Workflow{
		Steps: map[string]*Step{
			"A": {ID: "A"},
			"B": {ID: "B", DependsOn: []string{"A"}},
		},
	}
	run := &WorkflowRun{
		Steps: map[string]*StepRun{
			"A": {StepID: "A", Status: StepStatusFailed},
			"B": {StepID: "B", Status: StepStatusPending},
		},
	}
	skipDependentSteps(wfDef, run, "A")
	if run.Steps["B"].Status != StepStatusSkipped {
		t.Errorf("expected B skipped, got %s", run.Steps["B"].Status)
	}
	if run.Steps["B"].SkipReason != "dependency A failed" {
		t.Errorf("expected skip reason, got %q", run.Steps["B"].SkipReason)
	}
}

func TestSkipDependentSteps_Transitive(t *testing.T) {
	wfDef := &Workflow{
		Steps: map[string]*Step{
			"A": {ID: "A"},
			"B": {ID: "B", DependsOn: []string{"A"}},
			"C": {ID: "C", DependsOn: []string{"B"}},
		},
	}
	run := &WorkflowRun{
		Steps: map[string]*StepRun{
			"A": {StepID: "A", Status: StepStatusFailed},
			"B": {StepID: "B", Status: StepStatusPending},
			"C": {StepID: "C", Status: StepStatusPending},
		},
	}
	skipDependentSteps(wfDef, run, "A")
	if run.Steps["B"].Status != StepStatusSkipped {
		t.Errorf("expected B skipped, got %s", run.Steps["B"].Status)
	}
	if run.Steps["C"].Status != StepStatusSkipped {
		t.Errorf("expected C transitively skipped, got %s", run.Steps["C"].Status)
	}
}

func TestSkipDependentSteps_IndependentUnaffected(t *testing.T) {
	wfDef := &Workflow{
		Steps: map[string]*Step{
			"A": {ID: "A"},
			"B": {ID: "B", DependsOn: []string{"A"}},
			"C": {ID: "C"}, // no deps
		},
	}
	run := &WorkflowRun{
		Steps: map[string]*StepRun{
			"A": {StepID: "A", Status: StepStatusFailed},
			"B": {StepID: "B", Status: StepStatusPending},
			"C": {StepID: "C", Status: StepStatusPending},
		},
	}
	skipDependentSteps(wfDef, run, "A")
	if run.Steps["B"].Status != StepStatusSkipped {
		t.Errorf("expected B skipped, got %s", run.Steps["B"].Status)
	}
	if run.Steps["C"].Status != StepStatusPending {
		t.Errorf("expected C still pending (independent), got %s", run.Steps["C"].Status)
	}
}
