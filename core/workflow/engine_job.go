package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func makeJobPacket(traceID string, req *pb.JobRequest) *pb.BusPacket {
	return &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "workflow-engine",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload:         &pb.BusPacket_JobRequest{JobRequest: req},
	}
}

func (e *Engine) buildJobPayload(run *WorkflowRun, step *Step, item any) (map[string]any, error) {
	base := map[string]any{}
	if step != nil && len(step.Input) > 0 {
		scope := buildEvalScope(run, item)
		evaluated, err := evalTemplates(step.Input, scope)
		if err != nil {
			return nil, err
		}
		if m, ok := evaluated.(map[string]any); ok {
			base = m
		} else {
			return nil, fmt.Errorf("step input must be object, got %T", evaluated)
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
			"workflow_id": wfDef.ID,
			"run_id":      run.ID,
			"step_id":     stepID,
		},
		TenantId: run.OrgID,
	}
	if step.WorkerID != "" {
		req.Labels["worker_id"] = step.WorkerID
	}
	for k, v := range step.RouteLabels {
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		req.Labels[k] = v
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
