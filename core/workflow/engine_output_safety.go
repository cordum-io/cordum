package workflow

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"log/slog"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type jobRequestReader interface {
	GetJobRequest(ctx context.Context, jobID string) (*pb.JobRequest, error)
}

func isNilDependency(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func isNilRunLocker(locker RunLocker) bool {
	return isNilDependency(reflect.ValueOf(locker))
}

func isNilOutputSafety(checker model.OutputSafetyChecker) bool {
	return isNilDependency(reflect.ValueOf(checker))
}

func isNilJobRequestReader(reader jobRequestReader) bool {
	return isNilDependency(reflect.ValueOf(reader))
}

// checkStepOutputPolicy evaluates resolved output content against the persisted
// request that created the job. It returns true when the caller must not record
// the output. Missing dependencies and unknown decisions fail closed.
func (e *Engine) checkStepOutputPolicy(
	ctx context.Context,
	run *WorkflowRun,
	stepID string,
	stepRun *StepRun,
	res *pb.JobResult,
	output resolvedStepOutput,
) bool {
	if !hasJobResultResource(res) {
		return false
	}
	if isNilOutputSafety(e.outputSafety) {
		e.failStepOutput(ctx, run, stepID, stepRun, res, fmt.Errorf("output safety unavailable"))
		return true
	}
	request, err := e.loadOutputSafetyRequest(ctx, run, stepID, res)
	if err != nil {
		e.failStepOutput(ctx, run, stepID, stepRun, res, err)
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	evaluation := newWorkflowOutputEvaluation(res, request, output)
	record, err := e.outputSafety.EvaluateOutput(ctx, evaluation)
	if err != nil {
		slog.Error("step output policy check failed", "run_id", run.ID, "step_id", stepID)
		e.failStepOutput(ctx, run, stepID, stepRun, res, err)
		return true
	}
	return e.applyStepOutputDecision(ctx, run, stepID, stepRun, res, record)
}

func (e *Engine) loadOutputSafetyRequest(
	ctx context.Context,
	run *WorkflowRun,
	stepID string,
	res *pb.JobResult,
) (*pb.JobRequest, error) {
	if e == nil || isNilJobRequestReader(e.jobRequests) {
		return nil, fmt.Errorf("authoritative job request store unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	readCtx, cancel := context.WithTimeout(ctx, validationTimeout)
	defer cancel()
	request, err := e.jobRequests.GetJobRequest(readCtx, res.GetJobId())
	if err != nil {
		return nil, fmt.Errorf("load authoritative job request: %w", err)
	}
	if err := validateOutputRequestAuthority(run, stepID, res, request); err != nil {
		return nil, err
	}
	return request, nil
}

func validateOutputRequestAuthority(
	run *WorkflowRun,
	stepID string,
	res *pb.JobResult,
	request *pb.JobRequest,
) error {
	if run == nil || res == nil || request == nil {
		return fmt.Errorf("output authority context missing")
	}
	if strings.TrimSpace(request.GetTopic()) == "" {
		return fmt.Errorf("output authority topic missing")
	}
	expected := []struct {
		name, actual, trusted string
	}{
		{"job", request.GetJobId(), res.GetJobId()},
		{"tenant", request.GetTenantId(), run.OrgID},
		{"workflow", request.GetWorkflowId(), run.WorkflowID},
		{"run", request.GetLabels()["run_id"], run.ID},
		{"step", request.GetLabels()["step_id"], stepID},
	}
	for _, field := range expected {
		if field.trusted == "" || field.actual != field.trusted {
			return fmt.Errorf("%s authority mismatch", field.name)
		}
	}
	return validateOutputRequestMirrors(run, stepID, request)
}

func validateOutputRequestMirrors(run *WorkflowRun, stepID string, request *pb.JobRequest) error {
	mirrors := []struct {
		name, actual, trusted string
	}{
		{"env tenant", request.GetEnv()["tenant_id"], run.OrgID},
		{"env workflow", request.GetEnv()["workflow_id"], run.WorkflowID},
		{"env run", request.GetEnv()["run_id"], run.ID},
		{"env step", request.GetEnv()["step_id"], stepID},
		{"label workflow", request.GetLabels()["workflow_id"], run.WorkflowID},
	}
	for _, field := range mirrors {
		if field.actual != "" && field.actual != field.trusted {
			return fmt.Errorf("%s authority mismatch", field.name)
		}
	}
	if meta := request.GetMeta(); meta != nil &&
		meta.GetTenantId() != "" && meta.GetTenantId() != run.OrgID {
		return fmt.Errorf("metadata tenant authority mismatch")
	}
	identity := request.GetIdentity()
	if identity == nil {
		return nil
	}
	if identity.GetTenantId() != "" && identity.GetTenantId() != run.OrgID {
		return fmt.Errorf("identity tenant authority mismatch")
	}
	if identity.GetPrincipalId() != "" && identity.GetPrincipalId() != request.GetPrincipalId() {
		return fmt.Errorf("identity principal authority mismatch")
	}
	if identity.GetActorId() != "" && identity.GetActorId() != request.GetMeta().GetActorId() {
		return fmt.Errorf("identity actor authority mismatch")
	}
	return nil
}

func (e *Engine) applyStepOutputDecision(
	ctx context.Context,
	run *WorkflowRun,
	stepID string,
	stepRun *StepRun,
	res *pb.JobResult,
	record model.OutputSafetyRecord,
) bool {
	if record.Decision == model.OutputAllow {
		return false
	}
	if record.Decision != model.OutputQuarantine && record.Decision != model.OutputDeny {
		e.failStepOutput(ctx, run, stepID, stepRun, res, fmt.Errorf("unsupported output safety decision"))
		return true
	}
	now := time.Now().UTC()
	stepRun.Status = StepStatusFailed
	stepRun.CompletedAt = &now
	stepRun.Output = nil
	stepRun.Error = map[string]any{
		"code":    "output_quarantined",
		"message": record.Reason,
	}
	e.appendTimeline(ctx, run, "step_output_quarantined", stepID, res.JobId, string(stepRun.Status), "", record.Reason, nil)
	return true
}
