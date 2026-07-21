package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type workflowResourceResolver struct {
	content []byte
	media   string
	err     error
	calls   int
	trusted resource.TrustedContext
	ref     *agentv1.ResourceRef
}

func (r *workflowResourceResolver) Resolve(
	_ context.Context,
	ref *agentv1.ResourceRef,
	trusted resource.TrustedContext,
) (resource.ResolvedResource, error) {
	r.calls++
	r.ref = ref
	r.trusted = trusted
	return resource.ResolvedResource{Content: r.content, MediaType: r.media}, r.err
}

func TestResolveStepOutputUsesStructuredReferenceAndTrustedRun(t *testing.T) {
	resolver := &workflowResourceResolver{content: []byte(`{"ok":true}`), media: "application/json"}
	engine := &Engine{resourceReader: resourceio.Reader{Resolver: resolver}}
	run := &WorkflowRun{OrgID: "tenant-a"}
	ref := &pb.ResourceRef{ResolverId: "operator"}
	res := &pb.JobResult{JobId: "job-1", ResultRef: ref}

	output, err := engine.resolveStepOutput(context.Background(), run, res)
	if err != nil {
		t.Fatalf("resolveStepOutput() error = %v", err)
	}
	object, ok := output.value.(map[string]any)
	if !ok || object["ok"] != true {
		t.Fatalf("resolved output = %#v, want decoded JSON", output.value)
	}
	if resolver.ref != ref {
		t.Fatal("resolver did not receive the structured reference")
	}
	want := (resource.TrustedContext{TenantID: "tenant-a", JobID: "job-1"})
	if resolver.trusted != want {
		t.Fatalf("trusted context = %#v, want %#v", resolver.trusted, want)
	}
}

func TestResolveStepOutputLegacyRequiresExplicitCompatibility(t *testing.T) {
	mem, mini := newMemoryStore(t)
	defer mini.Close()
	defer mem.Close()
	if err := mem.PutResult(context.Background(), store.MakeResultKey("job-1"), []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("PutResult() error = %v", err)
	}
	engine := NewEngine(nil, nil).WithMemory(mem)
	run := &WorkflowRun{OrgID: "tenant-a"}
	res := &pb.JobResult{JobId: "job-1", ResultPtr: store.PointerForKey(store.MakeResultKey("job-1"))}

	if _, err := engine.resolveStepOutput(context.Background(), run, res); !errors.Is(err, resourceio.ErrStructuredRequired) {
		t.Fatalf("default legacy error = %v, want %v", err, resourceio.ErrStructuredRequired)
	}
	var observed resourceio.LegacyUse
	engine.WithLegacyResourceCompatibility(func(use resourceio.LegacyUse) { observed = use })
	output, err := engine.resolveStepOutput(context.Background(), run, res)
	if err != nil {
		t.Fatalf("compat resolveStepOutput() error = %v", err)
	}
	if object, ok := output.value.(map[string]any); !ok || object["ok"] != true {
		t.Fatalf("compat output = %#v, want decoded JSON", output.value)
	}
	want := (resourceio.LegacyUse{Component: "workflow.result", TenantID: "tenant-a", JobID: "job-1"})
	if observed != want {
		t.Fatalf("legacy observation = %#v, want %#v", observed, want)
	}
}

func TestResolveStepOutputRejectsCrossJobLegacyPointer(t *testing.T) {
	mem, mini := newMemoryStore(t)
	defer mini.Close()
	defer mem.Close()
	engine := NewEngine(nil, nil).WithMemory(mem).WithLegacyResourceCompatibility(nil)
	res := &pb.JobResult{JobId: "job-1", ResultPtr: store.PointerForKey(store.MakeResultKey("job-2"))}

	_, err := engine.resolveStepOutput(context.Background(), &WorkflowRun{OrgID: "tenant-a"}, res)
	if !errors.Is(err, resourceio.ErrLegacyScopeMismatch) {
		t.Fatalf("cross-job error = %v, want %v", err, resourceio.ErrLegacyScopeMismatch)
	}
}

func TestResolveStepOutputRejectsDualContentMismatch(t *testing.T) {
	mem, mini := newMemoryStore(t)
	defer mini.Close()
	defer mem.Close()
	if err := mem.PutResult(context.Background(), store.MakeResultKey("job-1"), []byte(`{"source":"legacy"}`)); err != nil {
		t.Fatalf("PutResult() error = %v", err)
	}
	resolver := &workflowResourceResolver{content: []byte(`{"source":"structured"}`), media: "application/json"}
	engine := NewEngine(nil, nil).WithMemory(mem).WithLegacyResourceCompatibility(nil)
	engine.resourceReader.Resolver = resolver
	res := &pb.JobResult{
		JobId: "job-1", ResultPtr: store.PointerForKey(store.MakeResultKey("job-1")),
		ResultRef: &pb.ResourceRef{ResolverId: "operator"},
	}

	_, err := engine.resolveStepOutput(context.Background(), &WorkflowRun{OrgID: "tenant-a"}, res)
	if !errors.Is(err, resourceio.ErrDualContentMismatch) {
		t.Fatalf("dual-content error = %v, want %v", err, resourceio.ErrDualContentMismatch)
	}
}

func TestProcessStepOutputFailsClosedWithoutResolver(t *testing.T) {
	engine := NewEngine(nil, nil)
	run := &WorkflowRun{OrgID: "tenant-a", Context: map[string]any{}}
	stepRun := &StepRun{StepID: "step-1", Status: StepStatusSucceeded, Output: "opaque-pointer"}
	res := &pb.JobResult{JobId: "job-1", ResultRef: &pb.ResourceRef{ResolverId: "operator"}}

	if engine.processStepOutput(context.Background(), run, "step-1", &Step{}, stepRun, res, true) {
		t.Fatal("processStepOutput() accepted a structured result without a resolver")
	}
	if stepRun.Status != StepStatusFailed {
		t.Fatalf("step status = %s, want %s", stepRun.Status, StepStatusFailed)
	}
	if stepRun.Output != nil {
		t.Fatalf("failed resource left opaque StepRun.Output = %#v", stepRun.Output)
	}
	if _, ok := run.Context["steps"]; ok {
		t.Fatal("failed resource resolution must not write an opaque reference into workflow context")
	}
}

func TestProcessStepOutputDoesNotExposeResolverError(t *testing.T) {
	resolver := &workflowResourceResolver{err: errors.New("failed redis://operator/tenant-secret")}
	engine := &Engine{resourceReader: resourceio.Reader{Resolver: resolver}}
	run := &WorkflowRun{OrgID: "tenant-a", Context: map[string]any{}}
	stepRun := &StepRun{StepID: "step-1", Status: StepStatusSucceeded}
	res := &pb.JobResult{JobId: "job-1", ResultRef: &pb.ResourceRef{ResolverId: "operator"}}

	if engine.processStepOutput(context.Background(), run, "step-1", &Step{}, stepRun, res, true) {
		t.Fatal("processStepOutput() accepted resolver failure")
	}
	message, _ := stepRun.Error["message"].(string)
	if strings.Contains(message, "tenant-secret") || message != "workflow result rejected" {
		t.Fatalf("unsafe resolver error exposed as %q", message)
	}
}

func TestProcessStepOutputAllowsOnlyBoundPointerInExplicitCompatibility(t *testing.T) {
	uses := 0
	engine := NewEngine(nil, nil).WithLegacyResourceCompatibility(func(resourceio.LegacyUse) { uses++ })
	run := &WorkflowRun{OrgID: "tenant-a", Context: map[string]any{}}
	stepRun := &StepRun{StepID: "step-1", Status: StepStatusSucceeded}
	res := &pb.JobResult{
		JobId: "job-1", ResultPtr: store.PointerForKey(store.MakeResultKey("job-1")),
	}

	if !engine.processStepOutput(context.Background(), run, "step-1", &Step{}, stepRun, res, true) {
		t.Fatalf("processStepOutput() rejected exact bound compatibility pointer: %#v", stepRun.Error)
	}
	if uses != 1 {
		t.Fatalf("legacy observations = %d, want 1", uses)
	}
	steps := run.Context["steps"].(map[string]any)
	entry := steps["step-1"].(map[string]any)
	if entry["result_ptr"] != res.ResultPtr {
		t.Fatalf("recorded result_ptr = %#v, want explicit compatibility pointer", entry["result_ptr"])
	}

	crossJob := &pb.JobResult{
		JobId: "job-1", ResultPtr: store.PointerForKey(store.MakeResultKey("job-2")),
	}
	other := &StepRun{StepID: "step-2", Status: StepStatusSucceeded}
	if engine.processStepOutput(context.Background(), run, "step-2", &Step{}, other, crossJob, true) {
		t.Fatal("processStepOutput() accepted a cross-job pointer")
	}
	if uses != 1 {
		t.Fatalf("cross-job attempt emitted compatibility observation; uses = %d", uses)
	}
}

func TestDecodeWorkflowResultHonorsDeclaredMediaType(t *testing.T) {
	tests := []struct {
		name      string
		mediaType string
		content   string
		want      any
		wantErr   bool
	}{
		{name: "json", mediaType: "application/json", content: `{"ok":true}`, want: map[string]any{"ok": true}},
		{name: "invalid json", mediaType: "application/json", content: `not-json`, wantErr: true},
		{name: "json-looking text", mediaType: "text/plain", content: `{"ok":true}`, want: `{"ok":true}`},
		{name: "unsupported structured media", mediaType: "application/octet-stream", content: `safe text`, wantErr: true},
		{name: "legacy heuristic", content: `{"ok":true}`, want: map[string]any{"ok": true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeWorkflowResult(resource.ResolvedResource{
				Content: []byte(test.content), MediaType: test.mediaType,
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("decodeWorkflowResult() error = %v, wantErr %v", err, test.wantErr)
			}
			if !test.wantErr && !valuesEqual(got, test.want) {
				t.Fatalf("decodeWorkflowResult() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestValidateResolvedStepOutputHonorsCallerCancellation(t *testing.T) {
	mem, mini := newMemoryStore(t)
	defer mini.Close()
	defer mem.Close()
	registry := schema.NewRegistryFromClient(mem.Client())
	if err := registry.Register(context.Background(), "output", []byte(`{"type":"object"}`)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	engine := NewEngine(nil, nil).WithSchemaRegistry(registry)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := engine.validateResolvedStepOutput(ctx, &Step{OutputSchemaID: "output"}, map[string]any{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("validateResolvedStepOutput() error = %v, want context cancellation", err)
	}
}

func valuesEqual(got, want any) bool {
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	return gotErr == nil && wantErr == nil && string(gotJSON) == string(wantJSON)
}
