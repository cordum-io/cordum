package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/memory"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type stubConfig struct {
	cfg map[string]any
}

func (s stubConfig) Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error) {
	return s.cfg, nil
}

func newMemoryStore(t *testing.T) (*memory.RedisStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := memory.NewRedisStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("memory store init: %v", err)
	}
	return store, srv
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

func TestValidateStepOutputInlineSchema(t *testing.T) {
	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer memStore.Close()

	engine := (&Engine{}).WithMemory(memStore)
	step := &Step{OutputSchema: map[string]any{"type": "object", "required": []any{"result"}}}

	key := memory.MakeResultKey("job-1")
	payload := map[string]any{"result": "ok"}
	data, _ := json.Marshal(payload)
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := memory.PointerForKey(key)
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
	if clone["k"].(map[string]any)["v"].(float64) != 1 {
		t.Fatalf("unexpected clone")
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
	defer memStore.Close()

	jobID := "job-ctx"
	key := memory.MakeResultKey(jobID)
	data, _ := json.Marshal(map[string]any{"result": "ok"})
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := memory.PointerForKey(key)
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
	defer memStore.Close()

	jobID := "job-inline"
	key := memory.MakeResultKey(jobID)
	data, _ := json.Marshal(map[string]any{"result": "ok"})
	if err := memStore.PutResult(context.Background(), key, data); err != nil {
		t.Fatalf("put result: %v", err)
	}
	ptr := memory.PointerForKey(key)
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
