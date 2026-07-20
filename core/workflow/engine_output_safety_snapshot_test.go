package workflow

import (
	"context"
	"reflect"
	"testing"

	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type mutatingWorkflowSafety struct {
	store         *store.RedisStore
	key           string
	replacement   []byte
	evaluateCalls int
	contentCalls  int
	scanned       []byte
	evaluation    *model.OutputEvaluateRequest
}

func (s *mutatingWorkflowSafety) EvaluateOutput(
	ctx context.Context,
	request *model.OutputEvaluateRequest,
) (model.OutputSafetyRecord, error) {
	s.evaluateCalls++
	s.evaluation = request
	s.scanned = append([]byte(nil), request.OutputContent...)
	if err := s.store.PutResult(ctx, s.key, s.replacement); err != nil {
		return model.OutputSafetyRecord{}, err
	}
	return model.OutputSafetyRecord{Decision: model.OutputAllow}, nil
}

func (*mutatingWorkflowSafety) CheckOutputMeta(
	*pb.JobResult,
	*pb.JobRequest,
) (model.OutputSafetyRecord, error) {
	return model.OutputSafetyRecord{Decision: model.OutputAllow}, nil
}

func (s *mutatingWorkflowSafety) CheckOutputContent(
	ctx context.Context,
	_ *pb.JobResult,
	_ *pb.JobRequest,
) (model.OutputSafetyRecord, error) {
	s.contentCalls++
	if err := s.store.PutResult(ctx, s.key, s.replacement); err != nil {
		return model.OutputSafetyRecord{}, err
	}
	content, err := s.store.GetResult(ctx, s.key)
	s.scanned = append([]byte(nil), content...)
	return model.OutputSafetyRecord{Decision: model.OutputAllow}, err
}

func TestProcessStepOutputScansValidatedLegacySnapshot(t *testing.T) {
	mem, mini := newMemoryStore(t)
	defer mini.Close()
	defer mem.Close()
	const jobID = "run-a:step-1@1"
	initial := []byte(`{"version":"validated"}`)
	replacement := []byte(`{"attacker":true}`)
	key := store.MakeResultKey(jobID)
	if err := mem.PutResult(context.Background(), key, initial); err != nil {
		t.Fatalf("PutResult() error = %v", err)
	}
	checker := &mutatingWorkflowSafety{store: mem, key: key, replacement: replacement}
	reader := &workflowJobRequestReader{req: authoritativeWorkflowRequest(jobID)}
	engine := NewEngine(nil, nil).WithMemory(mem).
		WithLegacyResourceCompatibility(nil).WithRunLocker(reader).WithOutputSafety(checker)
	run := authoritativeWorkflowRun()
	stepRun := &StepRun{StepID: "step-1", Status: StepStatusSucceeded}
	result := &pb.JobResult{JobId: jobID, ResultPtr: store.PointerForKey(key)}
	step := &Step{OutputSchema: map[string]any{
		"type": "object", "required": []any{"version"},
	}}

	if !engine.processStepOutput(context.Background(), run, "step-1", step, stepRun, result, true) {
		t.Fatalf("processStepOutput() rejected validated snapshot: %#v", stepRun.Error)
	}
	if checker.evaluateCalls != 1 || checker.contentCalls != 0 {
		t.Fatalf("safety calls evaluate/content = %d/%d, want 1/0", checker.evaluateCalls, checker.contentCalls)
	}
	assertWorkflowEvaluationSnapshot(t, checker.evaluation, initial, "application/json")
	if string(checker.scanned) != string(initial) {
		t.Fatalf("safety scanned %q, want validated snapshot %q", checker.scanned, initial)
	}
	stored, err := mem.GetResult(context.Background(), key)
	if err != nil || string(stored) != string(replacement) {
		t.Fatalf("mutation stimulus = %q, %v; want %q", stored, err, replacement)
	}
	entry := run.Context["steps"].(map[string]any)["step-1"].(map[string]any)
	output := entry["output"].(map[string]any)
	if output["version"] != "validated" || output["attacker"] != nil {
		t.Fatalf("recorded output = %#v, want validated snapshot", output)
	}
}

func TestProcessStepOutputResolvesStructuredSnapshotOnce(t *testing.T) {
	content := []byte(`{"ok":true}`)
	resolver := &workflowResourceResolver{content: content, media: "application/json"}
	checker := allowingWorkflowSafety()
	request := authoritativeWorkflowRequest("run-a:step-1@1")
	request.Labels["custom"] = "persisted"
	request.Meta.Capability = "write"
	request.Meta.Requires = []string{"network"}
	request.Meta.RiskTags = []string{"prod"}
	request.Meta.PackId = "pack-a"
	reader := &workflowJobRequestReader{req: request}
	engine := NewEngine(nil, nil).WithRunLocker(reader).WithOutputSafety(checker)
	engine.resourceReader = resourceio.Reader{Resolver: resolver}
	run := authoritativeWorkflowRun()
	stepRun := &StepRun{StepID: "step-1", Status: StepStatusSucceeded}
	result := &pb.JobResult{
		JobId: "run-a:step-1@1", ResultRef: &pb.ResourceRef{ResolverId: "cache"},
		ArtifactPtrs: []string{"artifact-a"}, ErrorCode: "warning", ErrorMessage: "review",
		WorkerId: "worker-a", ExecutionMs: 17,
	}

	if !engine.processStepOutput(context.Background(), run, "step-1", &Step{}, stepRun, result, true) {
		t.Fatalf("processStepOutput() rejected structured snapshot: %#v", stepRun.Error)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want one immutable read", resolver.calls)
	}
	assertWorkflowEvaluationSnapshot(t, checker.evaluation, content, "application/json")
	evaluation := checker.evaluation
	if evaluation.Tenant != "tenant-a" || evaluation.WorkflowID != "workflow-a" ||
		evaluation.StepID != "step-1" || evaluation.PrincipalID != "principal-a" ||
		evaluation.WorkerID != "worker-a" || evaluation.ExecutionMs != 17 ||
		evaluation.ErrorCode != "warning" || evaluation.ErrorMessage != "review" ||
		evaluation.PackID != "pack-a" || evaluation.Labels["custom"] != "persisted" {
		t.Fatalf("incomplete persisted evaluation context: %#v", evaluation)
	}
	if !reflect.DeepEqual(evaluation.ArtifactPtrs, []string{"artifact-a"}) ||
		!reflect.DeepEqual(evaluation.Capabilities, []string{"write", "network"}) ||
		!reflect.DeepEqual(evaluation.RiskTags, []string{"prod"}) {
		t.Fatalf("incomplete evaluation lists: %#v", evaluation)
	}
}
