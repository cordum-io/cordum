package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type workflowJobRequestReader struct {
	req   *pb.JobRequest
	err   error
	calls int
	jobID string
}

func (r *workflowJobRequestReader) GetJobRequest(_ context.Context, jobID string) (*pb.JobRequest, error) {
	r.calls++
	r.jobID = jobID
	return r.req, r.err
}

func (*workflowJobRequestReader) TryAcquireLock(context.Context, string, time.Duration) (string, error) {
	return "lock-token", nil
}

func (*workflowJobRequestReader) ReleaseLock(context.Context, string, string) error { return nil }

type workflowOutputSafetyProbe struct {
	record        model.OutputSafetyRecord
	err           error
	evaluateCalls int
	contentCalls  int
	metaCalls     int
	evaluation    *model.OutputEvaluateRequest
	result        *pb.JobResult
	request       *pb.JobRequest
}

func (p *workflowOutputSafetyProbe) EvaluateOutput(
	_ context.Context,
	request *model.OutputEvaluateRequest,
) (model.OutputSafetyRecord, error) {
	p.evaluateCalls++
	p.evaluation = request
	return p.record, p.err
}

func (p *workflowOutputSafetyProbe) CheckOutputMeta(
	*pb.JobResult,
	*pb.JobRequest,
) (model.OutputSafetyRecord, error) {
	p.metaCalls++
	return p.record, p.err
}

func (p *workflowOutputSafetyProbe) CheckOutputContent(
	_ context.Context,
	result *pb.JobResult,
	request *pb.JobRequest,
) (model.OutputSafetyRecord, error) {
	p.contentCalls++
	p.result = result
	p.request = request
	return p.record, p.err
}

func TestProcessStepOutputUsesPersistedRequestForContentSafety(t *testing.T) {
	jobID := "run-a:step-1@1"
	request := authoritativeWorkflowRequest(jobID)
	reader := &workflowJobRequestReader{req: request}
	checker := &workflowOutputSafetyProbe{record: model.OutputSafetyRecord{Decision: model.OutputAllow}}
	engine := structuredOutputEngine(reader, checker)
	run := authoritativeWorkflowRun()
	stepRun := &StepRun{StepID: "step-1", Status: StepStatusSucceeded}
	result := &pb.JobResult{JobId: jobID, ResultRef: &agentv1.ResourceRef{ResolverId: "cache"}}

	if !engine.processStepOutput(context.Background(), run, "step-1", &Step{}, stepRun, result, true) {
		t.Fatalf("processStepOutput() rejected allowed output: %#v", stepRun.Error)
	}
	if reader.calls != 1 || reader.jobID != jobID {
		t.Fatalf("request reads = %d for %q, want one for %q", reader.calls, reader.jobID, jobID)
	}
	if checker.evaluateCalls != 1 || checker.contentCalls != 0 || checker.metaCalls != 0 {
		t.Fatalf("safety calls evaluate/content/meta = %d/%d/%d, want 1/0/0",
			checker.evaluateCalls, checker.contentCalls, checker.metaCalls)
	}
	assertWorkflowEvaluationSnapshot(t, checker.evaluation, []byte(`{"ok":true}`), "application/json")
	if checker.evaluation.JobID != result.GetJobId() || checker.evaluation.Topic != request.GetTopic() {
		t.Fatalf("safety authority = job %q topic %q", checker.evaluation.JobID, checker.evaluation.Topic)
	}
	steps, ok := run.Context["steps"].(map[string]any)
	if !ok || steps["step-1"] == nil {
		t.Fatalf("allowed output was not recorded: %#v", run.Context)
	}
}

func TestProcessStepOutputFailsClosedWhenSafetyUnavailable(t *testing.T) {
	tests := map[string]struct {
		reader  *workflowJobRequestReader
		checker *workflowOutputSafetyProbe
	}{
		"missing request reader": {checker: allowingWorkflowSafety()},
		"request read error": {
			reader:  &workflowJobRequestReader{err: errors.New("redis unavailable")},
			checker: allowingWorkflowSafety(),
		},
		"request missing": {
			reader:  &workflowJobRequestReader{},
			checker: allowingWorkflowSafety(),
		},
		"missing safety checker": {reader: &workflowJobRequestReader{req: authoritativeWorkflowRequest("run-a:step-1@1")}},
		"safety check error": {
			reader:  &workflowJobRequestReader{req: authoritativeWorkflowRequest("run-a:step-1@1")},
			checker: &workflowOutputSafetyProbe{err: errors.New("kernel unavailable")},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assertWorkflowOutputFailsClosed(t, test.reader, test.checker)
		})
	}
}

func TestProcessStepOutputRejectsMismatchedPersistedAuthority(t *testing.T) {
	tests := map[string]func(*pb.JobRequest){
		"topic missing":      func(req *pb.JobRequest) { req.Topic = " " },
		"job":                func(req *pb.JobRequest) { req.JobId = "run-a:other@1" },
		"tenant":             func(req *pb.JobRequest) { req.TenantId = "tenant-b" },
		"tenant whitespace":  func(req *pb.JobRequest) { req.TenantId = " tenant-a" },
		"workflow":           func(req *pb.JobRequest) { req.WorkflowId = "workflow-b" },
		"run":                func(req *pb.JobRequest) { req.Labels["run_id"] = "run-b" },
		"step":               func(req *pb.JobRequest) { req.Labels["step_id"] = "step-2" },
		"env tenant":         func(req *pb.JobRequest) { req.Env["tenant_id"] = "tenant-b" },
		"env workflow":       func(req *pb.JobRequest) { req.Env["workflow_id"] = "workflow-b" },
		"env run":            func(req *pb.JobRequest) { req.Env["run_id"] = "run-b" },
		"env step":           func(req *pb.JobRequest) { req.Env["step_id"] = "step-2" },
		"label workflow":     func(req *pb.JobRequest) { req.Labels["workflow_id"] = "workflow-b" },
		"metadata tenant":    func(req *pb.JobRequest) { req.Meta.TenantId = "tenant-b" },
		"identity tenant":    func(req *pb.JobRequest) { req.Identity.TenantId = "tenant-b" },
		"identity principal": func(req *pb.JobRequest) { req.Identity.PrincipalId = "principal-b" },
		"identity actor":     func(req *pb.JobRequest) { req.Identity.ActorId = "actor-b" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := authoritativeWorkflowRequest("run-a:step-1@1")
			mutate(request)
			checker := allowingWorkflowSafety()
			assertWorkflowOutputFailsClosed(t, &workflowJobRequestReader{req: request}, checker)
			if checker.evaluateCalls != 0 || checker.contentCalls != 0 || checker.metaCalls != 0 {
				t.Fatalf("mismatched authority reached safety checker: %#v", checker)
			}
		})
	}
}

func TestProcessStepOutputRejectsNonAllowSafetyDecisions(t *testing.T) {
	decisions := []model.OutputDecision{
		model.OutputDeny,
		model.OutputQuarantine,
		model.OutputRedact,
		model.OutputDecision("UNKNOWN"),
		"",
	}
	for _, decision := range decisions {
		t.Run(string(decision), func(t *testing.T) {
			reader := &workflowJobRequestReader{req: authoritativeWorkflowRequest("run-a:step-1@1")}
			checker := &workflowOutputSafetyProbe{record: model.OutputSafetyRecord{Decision: decision}}
			assertWorkflowOutputFailsClosed(t, reader, checker)
			if checker.evaluateCalls != 1 || checker.contentCalls != 0 || checker.metaCalls != 0 {
				t.Fatalf("safety calls evaluate/content/meta = %d/%d/%d, want 1/0/0",
					checker.evaluateCalls, checker.contentCalls, checker.metaCalls)
			}
		})
	}
}

func TestProcessStepOutputFailsClosedForTypedNilDependencies(t *testing.T) {
	t.Run("checker", func(t *testing.T) {
		reader := &workflowJobRequestReader{req: authoritativeWorkflowRequest("run-a:step-1@1")}
		engine := structuredOutputEngine(reader, nil)
		var checker *workflowOutputSafetyProbe
		engine.WithOutputSafety(checker)
		assertWorkflowEngineOutputFailsClosed(t, engine)
	})
	t.Run("request reader", func(t *testing.T) {
		engine := structuredOutputEngine(nil, allowingWorkflowSafety())
		var reader *workflowJobRequestReader
		engine.WithRunLocker(reader)
		assertWorkflowEngineOutputFailsClosed(t, engine)
	})
}

func structuredOutputEngine(reader *workflowJobRequestReader, checker *workflowOutputSafetyProbe) *Engine {
	resolver := &workflowResourceResolver{content: []byte(`{"ok":true}`), media: "application/json"}
	engine := NewEngine(nil, nil)
	if reader != nil {
		engine = engine.WithRunLocker(reader)
	}
	if checker != nil {
		engine = engine.WithOutputSafety(checker)
	}
	engine.resourceReader = resourceio.Reader{Resolver: resolver}
	return engine
}

func authoritativeWorkflowRequest(jobID string) *pb.JobRequest {
	return &pb.JobRequest{
		JobId: jobID, Topic: "job.demo", TenantId: "tenant-a", WorkflowId: "workflow-a",
		PrincipalId: "principal-a",
		Env: map[string]string{
			"tenant_id": "tenant-a", "workflow_id": "workflow-a", "run_id": "run-a", "step_id": "step-1",
		},
		Labels:   map[string]string{"workflow_id": "workflow-a", "run_id": "run-a", "step_id": "step-1"},
		Meta:     &agentv1.JobMetadata{TenantId: "tenant-a", ActorId: "actor-a"},
		Identity: &agentv1.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a"},
	}
}

func authoritativeWorkflowRun() *WorkflowRun {
	return &WorkflowRun{
		ID: "run-a", WorkflowID: "workflow-a", OrgID: "tenant-a", Context: map[string]any{},
	}
}

func allowingWorkflowSafety() *workflowOutputSafetyProbe {
	return &workflowOutputSafetyProbe{record: model.OutputSafetyRecord{Decision: model.OutputAllow}}
}

func assertWorkflowEvaluationSnapshot(
	t *testing.T,
	request *model.OutputEvaluateRequest,
	content []byte,
	mediaType string,
) {
	t.Helper()
	if request == nil {
		t.Fatal("missing output evaluation request")
	}
	sum := sha256.Sum256(content)
	wantHash := "sha256:" + hex.EncodeToString(sum[:])
	if string(request.OutputContent) != string(content) || request.OutputSizeBytes != int64(len(content)) ||
		request.ContentHash != wantHash || request.ContentType != mediaType {
		t.Fatalf("evaluation snapshot = content %q size %d hash %q media %q",
			request.OutputContent, request.OutputSizeBytes, request.ContentHash, request.ContentType)
	}
	if request.ResultPtr != "" || request.ResultRef != nil {
		t.Fatalf("evaluation retained mutable resource location: ptr %q ref %#v", request.ResultPtr, request.ResultRef)
	}
}

func assertWorkflowOutputFailsClosed(
	t *testing.T,
	reader *workflowJobRequestReader,
	checker *workflowOutputSafetyProbe,
) {
	t.Helper()
	engine := structuredOutputEngine(reader, checker)
	assertWorkflowEngineOutputFailsClosed(t, engine)
}

func assertWorkflowEngineOutputFailsClosed(t *testing.T, engine *Engine) {
	t.Helper()
	run := authoritativeWorkflowRun()
	stepRun := &StepRun{StepID: "step-1", Status: StepStatusSucceeded, Output: "stale"}
	result := &pb.JobResult{
		JobId: "run-a:step-1@1", ResultRef: &agentv1.ResourceRef{ResolverId: "cache"},
	}
	if engine.processStepOutput(context.Background(), run, "step-1", &Step{}, stepRun, result, true) {
		t.Fatal("processStepOutput() accepted output without authoritative safety")
	}
	if stepRun.Status != StepStatusFailed || stepRun.Output != nil {
		t.Fatalf("failed step = status %q output %#v", stepRun.Status, stepRun.Output)
	}
	if _, exists := run.Context["steps"]; exists {
		t.Fatalf("rejected output mutated workflow context: %#v", run.Context)
	}
}
