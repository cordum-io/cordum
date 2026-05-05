package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policylabels"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// publishWithTrace publishes a BusPacket, propagating trace context through
// NATS headers when the bus supports it (ContextPublisher interface).
func (e *Engine) publishWithTrace(ctx context.Context, subject string, packet *pb.BusPacket) error {
	if cp, ok := e.bus.(model.ContextPublisher); ok {
		return cp.PublishWithContext(ctx, subject, packet)
	}
	return e.bus.Publish(subject, packet)
}

func makeJobPacket(traceID string, req *pb.JobRequest) *pb.BusPacket {
	return &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "workflow-engine",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload:         &pb.BusPacket_JobRequest{JobRequest: req},
	}
}

func (e *Engine) evaluateStructuredStepInput(run *WorkflowRun, step *Step, scope map[string]any, inputKind string) (map[string]any, error) {
	if step == nil || len(step.Input) == 0 {
		return map[string]any{}, nil
	}

	var runInputKeys, stepInputKeys []string
	for k := range run.Input {
		runInputKeys = append(runInputKeys, k)
	}
	for k := range step.Input {
		stepInputKeys = append(stepInputKeys, k)
	}
	slog.Info("evaluateStructuredStepInput", "run_id", run.ID, "input_kind", inputKind, "run_input", runInputKeys, "step_input", stepInputKeys, "scope_input_type", fmt.Sprintf("%T", scope["input"]))

	evaluated, err := evalTemplates(step.Input, scope)
	if err != nil {
		return nil, err
	}
	base, ok := evaluated.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s input must be object, got %T", inputKind, evaluated)
	}

	reqSet := requiredFromInlineSchema(step.InputSchema)
	for k, raw := range step.Input {
		if _, exists := base[k]; exists {
			continue
		}
		if s, ok := raw.(string); ok && strings.Contains(s, "${") {
			if reqSet[k] {
				return nil, fmt.Errorf(
					"%s input field %q has template %q that resolved to nil — "+
						"check that run.Input contains the expected data (run input keys: %v)",
					inputKind, k, s, runInputKeys)
			}
		}
	}
	return base, nil
}

func (e *Engine) buildJobPayload(run *WorkflowRun, step *Step, item any) (map[string]any, error) {
	base := map[string]any{}
	if step != nil && len(step.Input) > 0 {
		var err error
		base, err = e.evaluateStructuredStepInput(run, step, buildEvalScope(run, item), "step")
		if err != nil {
			return nil, err
		}
	} else if run != nil && len(run.Input) > 0 {
		base = run.Input
	}
	// Avoid overflow in capacity arithmetic on extreme map sizes.
	capHint := len(base)
	if capHint < int(^uint(0)>>1) {
		capHint++
	}
	out := make(map[string]any, capHint)
	for k, v := range base {
		out[k] = v
	}
	if item != nil {
		if _, ok := out["item"]; !ok {
			out["item"] = item
		}
	}
	if err := e.validateStepInput(step, out); err != nil {
		return nil, err
	}
	return out, nil
}

// buildApprovalPayload constructs the persisted approval-gate context contract.
// The payload is intentionally split into stable workflow metadata plus a
// structured decision block so API/UI consumers can render human-meaningful
// fields without reverse-engineering scheduler labels. Unlike regular worker
// dispatch, approval steps do not fall back to the entire run input when
// step.Input is empty; legacy approvals instead receive a metadata-only
// envelope so the contract stays deterministic and auditable.
func (e *Engine) buildApprovalPayload(wfDef *Workflow, run *WorkflowRun, step *Step, stepID string, requestedAt time.Time) (map[string]any, error) {
	if run == nil {
		run = &WorkflowRun{}
	}
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	}

	decision, err := e.evaluateStructuredStepInput(run, step, buildEvalScope(run, nil), "approval")
	if err != nil {
		return nil, err
	}
	if err := e.validateStepInput(step, decision); err != nil {
		return nil, err
	}

	stepName := stepID
	if step != nil && strings.TrimSpace(step.Name) != "" {
		stepName = strings.TrimSpace(step.Name)
	}

	envelope := ApprovalContextEnvelope{
		Kind:    ApprovalContextKindWorkflow,
		Version: ApprovalContextVersionV1,
		Workflow: ApprovalWorkflowContext{
			WorkflowID:   wfDef.ID,
			WorkflowName: wfDef.Name,
			RunID:        run.ID,
			StepID:       stepID,
			StepName:     stepName,
			RequestedAt:  requestedAt.UTC().Format(time.RFC3339Nano),
			TriggeredBy:  strings.TrimSpace(run.TriggeredBy),
			TenantID:     strings.TrimSpace(run.OrgID),
			TeamID:       strings.TrimSpace(run.TeamID),
		},
		Decision: decision,
	}
	return envelope.AsMap(), nil
}

func (e *Engine) putJobContext(ctx context.Context, jobID string, payload map[string]any) (string, error) {
	if e.mem == nil {
		return "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal step input: %w", err)
	}
	key := store.MakeContextKey(jobID)
	if err := e.mem.PutContext(ctx, key, data); err != nil {
		return "", fmt.Errorf("store step context: %w", err)
	}
	return store.PointerForKey(key), nil
}

func (e *Engine) buildJobRequest(ctx context.Context, wfDef *Workflow, run *WorkflowRun, step *Step, stepID, jobID string) *pb.JobRequest {
	if run == nil {
		run = &WorkflowRun{}
	}
	subject := step.Topic
	if subject == "" {
		subject = "job.workflow." + wfDef.ID
	}

	priority := pb.JobPriority_JOB_PRIORITY_BATCH
	if run.Input != nil {
		if raw, ok := run.Input["priority"]; ok {
			if s, ok := raw.(string); ok {
				switch strings.ToLower(strings.TrimSpace(s)) {
				case "critical":
					priority = pb.JobPriority_JOB_PRIORITY_CRITICAL
				case "interactive":
					priority = pb.JobPriority_JOB_PRIORITY_INTERACTIVE
				case "batch":
					priority = pb.JobPriority_JOB_PRIORITY_BATCH
				}
			}
		}
	}
	memoryID := "run:" + run.ID
	if run.Input != nil {
		if raw, ok := run.Input["memory_id"]; ok {
			if s, ok := raw.(string); ok {
				if trimmed := store.NormalizeMemoryID(s); trimmed != "" {
					memoryID = trimmed
				}
			}
		}
	}
	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      subject,
		Priority:   priority,
		AdapterId:  step.WorkerID,
		WorkflowId: wfDef.ID,
		MemoryId:   memoryID,
		Env: map[string]string{
			"workflow_id":  wfDef.ID,
			"run_id":       run.ID,
			"step_id":      stepID,
			"tenant_id":    run.OrgID,
			"team_id":      run.TeamID,
			"memory_id":    memoryID,
			"context_mode": defaultContextModeForTopic(subject),
		},
		Labels: map[string]string{
			"workflow_id":                   wfDef.ID,
			"run_id":                        run.ID,
			"step_id":                       stepID,
			policylabels.PolicyAttachmentID: policylabels.JobAttachmentID(jobID),
			"_source":                       "workflow",
		},
		TenantId: run.OrgID,
	}
	if step.WorkerID != "" {
		req.Labels["worker_id"] = step.WorkerID
	}
	if len(step.RouteLabels) > 0 {
		scope := buildEvalScope(run, nil)
		for k, v := range step.RouteLabels {
			if req.Labels == nil {
				req.Labels = map[string]string{}
			}
			resolved, err := evalTemplates(v, scope)
			if err != nil {
				req.Labels[k] = v // fallback to raw value on error
			} else {
				req.Labels[k] = fmt.Sprint(resolved)
			}
		}
	}
	if step.TimeoutSec > 0 {
		req.Budget = &pb.Budget{
			DeadlineMs: step.TimeoutSec * 1000,
		}
	}
	if meta := buildStepMetadata(run, step); meta != nil {
		req.Meta = meta
		if req.PrincipalId == "" && meta.GetActorId() != "" {
			req.PrincipalId = meta.GetActorId()
		}
	}
	if run.DryRun || run.Metadata["dry_run"] == "true" {
		req.Env["dry_run"] = "true"
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		req.Labels["dry_run"] = "true"
	}
	if e.config != nil {
		if cfg, err := e.config.Effective(ctx, run.OrgID, run.TeamID, wfDef.ID, stepID); err == nil && cfg != nil {
			if data, err := json.Marshal(cfg); err == nil {
				if req.Env == nil {
					req.Env = map[string]string{}
				}
				req.Env["CORDUM_EFFECTIVE_CONFIG"] = string(data)
			}
		}
	}
	return req
}

func buildStepMetadata(run *WorkflowRun, step *Step) *pb.JobMetadata {
	if run == nil && (step == nil || step.Meta == nil) {
		return nil
	}
	meta := &pb.JobMetadata{}
	if run != nil {
		if tenant := strings.TrimSpace(run.OrgID); tenant != "" {
			meta.TenantId = tenant
		}
	}
	if step == nil || step.Meta == nil {
		if meta.TenantId == "" {
			return nil
		}
		return meta
	}
	sm := step.Meta
	meta.ActorId = strings.TrimSpace(sm.ActorId)
	meta.ActorType = actorTypeFromString(sm.ActorType)
	meta.IdempotencyKey = strings.TrimSpace(sm.IdempotencyKey)
	meta.PackId = strings.TrimSpace(sm.PackId)
	meta.Capability = strings.TrimSpace(sm.Capability)
	meta.RiskTags = cleanStrings(sm.RiskTags)
	meta.Requires = cleanStrings(sm.Requires)
	if len(sm.Labels) > 0 {
		meta.Labels = cloneStringMap(sm.Labels)
	}
	if metaEmpty(meta) {
		return nil
	}
	return meta
}

// buildApprovalGateRequest creates a lightweight job request for workflow
// approval steps. The job is dispatched to the approval gate topic so the
// scheduler places it in APPROVAL_REQUIRED state. Once approved via the
// unified /approvals/{job_id}/approve endpoint, the scheduler auto-completes
// the job and the result flows back through HandleJobResult.
func (e *Engine) buildApprovalGateRequest(ctx context.Context, wfDef *Workflow, run *WorkflowRun, step *Step, stepID, jobID string) *pb.JobRequest {
	if run == nil {
		run = &WorkflowRun{}
	}
	req := e.buildJobRequest(ctx, wfDef, run, step, stepID, jobID)
	req.Topic = capsdk.SubjectWorkflowApprovalGate
	req.Priority = pb.JobPriority_JOB_PRIORITY_INTERACTIVE
	req.AdapterId = ""
	if req.Env == nil {
		req.Env = map[string]string{}
	}
	req.Env["context_mode"] = defaultContextModeForTopic(req.Topic)
	if req.Labels == nil {
		req.Labels = map[string]string{}
	}
	req.Labels["gate_type"] = "workflow_approval"
	if req.Meta == nil {
		req.Meta = &pb.JobMetadata{}
	}
	req.Meta.IdempotencyKey = fmt.Sprintf("wf:%s:%s:%d:approval", run.ID, stepID, 1)
	return req
}

// requiredFromInlineSchema extracts the set of required field names from an
// inline JSON Schema object. Returns nil if the schema is nil or has no
// "required" key.
func requiredFromInlineSchema(schema map[string]any) map[string]bool {
	if schema == nil {
		return nil
	}
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make(map[string]bool, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out[s] = true
		}
	}
	return out
}
