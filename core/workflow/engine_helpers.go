package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// collectDependencies recursively gathers all transitive dependencies for a step.
func collectDependencies(wfDef *Workflow, stepID string, deps map[string]struct{}) {
	if wfDef == nil || stepID == "" {
		return
	}
	step := wfDef.Steps[stepID]
	if step == nil {
		return
	}
	for _, dep := range step.DependsOn {
		if dep == "" {
			continue
		}
		if _, ok := deps[dep]; ok {
			continue
		}
		deps[dep] = struct{}{}
		collectDependencies(wfDef, dep, deps)
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func cloneContextForDeps(ctx map[string]any, deps map[string]struct{}) map[string]any {
	if ctx == nil {
		return nil
	}
	out := cloneMap(ctx)
	stepsRaw, ok := out["steps"]
	if !ok {
		return out
	}
	steps, ok := stepsRaw.(map[string]any)
	if !ok {
		return out
	}
	filtered := map[string]any{}
	for dep := range deps {
		if val, ok := steps[dep]; ok {
			filtered[dep] = val
		}
	}
	out["steps"] = filtered
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func cloneStepRun(sr *StepRun) *StepRun {
	if sr == nil {
		return nil
	}
	data, err := json.Marshal(sr)
	if err != nil {
		return &StepRun{StepID: sr.StepID, Status: sr.Status}
	}
	var out StepRun
	if err := json.Unmarshal(data, &out); err != nil {
		return &StepRun{StepID: sr.StepID, Status: sr.Status}
	}
	return &out
}

func getContextPath(ctx map[string]any, path string) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur any = ctx
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func deleteContextPath(ctx map[string]any, path string) {
	if ctx == nil {
		return
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	parts := strings.Split(path, ".")
	cur := ctx
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		if i == len(parts)-1 {
			delete(cur, part)
			return
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}

func setContextPath(ctx map[string]any, path string, value any) error {
	if ctx == nil {
		return nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	cur := ctx
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("invalid context path")
		}
		if i == len(parts)-1 {
			cur[part] = value
			return nil
		}
		next, ok := cur[part].(map[string]any)
		if !ok || next == nil {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
	return nil
}

func depsSatisfied(step *Step, run *WorkflowRun) bool {
	if step == nil || len(step.DependsOn) == 0 {
		return true
	}
	// Steps explicitly activated by on_error (have error context in input)
	// bypass normal dependency checks so the error handler can run immediately.
	if sr := run.Steps[step.ID]; sr != nil && sr.Status == StepStatusPending && sr.Input != nil {
		if _, hasErr := sr.Input["error"]; hasErr {
			return true
		}
	}
	for _, dep := range step.DependsOn {
		sr, ok := run.Steps[dep]
		if !ok || sr.Status != StepStatusSucceeded {
			return false
		}
	}
	return true
}

// isOnErrorTarget returns true if stepID is referenced as an OnError target by any step.
func isOnErrorTarget(wfDef *Workflow, stepID string) bool {
	if wfDef == nil {
		return false
	}
	for _, s := range wfDef.Steps {
		if s != nil && s.OnError == stepID {
			return true
		}
	}
	return false
}

func splitJobID(jobID string) (runID, stepID string) {
	parts := strings.SplitN(jobID, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	runID = parts[0]
	stepID = parts[1]
	if at := strings.LastIndex(stepID, "@"); at > 0 {
		stepID = stepID[:at]
	}
	return
}

func defaultContextModeForTopic(topic string) string {
	return "raw"
}

func actorTypeFromString(raw string) pb.ActorType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "human":
		return pb.ActorType_ACTOR_TYPE_HUMAN
	case "service":
		return pb.ActorType_ACTOR_TYPE_SERVICE
	default:
		return pb.ActorType_ACTOR_TYPE_UNSPECIFIED
	}
}

func cleanStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func metaEmpty(meta *pb.JobMetadata) bool {
	if meta == nil {
		return true
	}
	return strings.TrimSpace(meta.TenantId) == "" &&
		strings.TrimSpace(meta.ActorId) == "" &&
		meta.ActorType == pb.ActorType_ACTOR_TYPE_UNSPECIFIED &&
		strings.TrimSpace(meta.IdempotencyKey) == "" &&
		strings.TrimSpace(meta.Capability) == "" &&
		len(meta.RiskTags) == 0 &&
		len(meta.Requires) == 0 &&
		strings.TrimSpace(meta.PackId) == "" &&
		len(meta.Labels) == 0
}

func splitForEachStep(stepID string) (base string, child string) {
	idx := strings.Index(stepID, "[")
	if idx == -1 {
		return stepID, ""
	}
	return stepID[:idx], stepID
}

func collectParallelChildOwners(wfDef *Workflow) map[string]string {
	owners := map[string]string{}
	if wfDef == nil {
		return owners
	}
	for parentID, step := range wfDef.Steps {
		if step == nil || step.Type != StepTypeParallel || step.Input == nil {
			continue
		}
		rawChildren, ok := step.Input["steps"]
		if !ok {
			continue
		}
		childIDs, err := parseParallelStepIDs(rawChildren)
		if err != nil {
			continue
		}
		for _, childID := range childIDs {
			if childID == "" || childID == parentID {
				continue
			}
			if _, exists := owners[childID]; !exists {
				owners[childID] = parentID
			}
		}
	}
	return owners
}

func collectLoopBodyOwners(wfDef *Workflow) map[string]string {
	owners := map[string]string{}
	if wfDef == nil {
		return owners
	}
	for parentID, step := range wfDef.Steps {
		if step == nil || step.Type != StepTypeLoop || step.Input == nil {
			continue
		}
		bodyStepID, _, err := loopInputString(step.Input, "body_step", "body")
		if err != nil {
			continue
		}
		if bodyStepID == "" || bodyStepID == parentID {
			continue
		}
		if _, exists := owners[bodyStepID]; !exists {
			owners[bodyStepID] = parentID
		}
	}
	return owners
}

func parsePositiveInt(value any) (int, error) {
	switch v := value.(type) {
	case int:
		if v > 0 {
			return v, nil
		}
	case int32:
		if v > 0 {
			return int(v), nil
		}
	case int64:
		if v > 0 {
			return int(v), nil
		}
	case float64:
		if v > 0 && math.Mod(v, 1) == 0 {
			return int(v), nil
		}
	case float32:
		fv := float64(v)
		if fv > 0 && math.Mod(fv, 1) == 0 {
			return int(fv), nil
		}
	case json.Number:
		if i64, err := v.Int64(); err == nil && i64 > 0 {
			return int(i64), nil
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil && n > 0 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("expected positive integer, got %v", value)
}

func isTerminalStepStatus(status StepStatus) bool {
	switch status {
	case StepStatusSucceeded, StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
		return true
	default:
		return false
	}
}

func parseAttempt(jobID string) int {
	at := strings.LastIndex(jobID, "@")
	if at == -1 || at == len(jobID)-1 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(jobID[at+1:]))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func isTerminalRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled, RunStatusTimedOut:
		return true
	default:
		return false
	}
}
