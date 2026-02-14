package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

const (
	ToolSubmitJob       = "cordum_submit_job"
	ToolCancelJob       = "cordum_cancel_job"
	ToolTriggerWorkflow = "cordum_trigger_workflow"
	ToolApproveJob      = "cordum_approve_job"
	ToolRejectJob       = "cordum_reject_job"
	ToolQueryPolicy     = "cordum_query_policy"
)

// ServiceBridge abstracts backend operations for MCP tool handlers.
type ServiceBridge interface {
	SubmitJob(ctx context.Context, req SubmitJobInput) (*SubmitJobOutput, error)
	CancelJob(ctx context.Context, jobID string, reason string) error
	TriggerWorkflow(ctx context.Context, req TriggerWorkflowInput) (*TriggerOutput, error)
	ApproveJob(ctx context.Context, jobID string, note string) error
	RejectJob(ctx context.Context, jobID string, reason string) error
	SimulatePolicy(ctx context.Context, req PolicySimInput) (*PolicySimOutput, error)
}

type SubmitJobInput struct {
	Prompt     string
	Topic      string
	Priority   string
	Capability string
	RiskTags   []string
	Labels     map[string]string
	MemoryID   string
	PackID     string
}

type SubmitJobOutput struct {
	JobID   string `json:"job_id"`
	TraceID string `json:"trace_id,omitempty"`
}

type TriggerWorkflowInput struct {
	WorkflowID     string
	Input          map[string]any
	DryRun         bool
	IdempotencyKey string
}

type TriggerOutput struct {
	RunID      string `json:"run_id"`
	WorkflowID string `json:"workflow_id"`
}

type PolicySimInput struct {
	Topic      string
	Priority   string
	Capability string
	RiskTags   []string
	Labels     map[string]string
}

type PolicySimOutput struct {
	Decision     string           `json:"decision"`
	Reason       string           `json:"reason,omitempty"`
	RuleID       string           `json:"rule_id,omitempty"`
	Constraints  map[string]any   `json:"constraints,omitempty"`
	Remediations []map[string]any `json:"remediations,omitempty"`
}

type submitJobArgs struct {
	Prompt     string            `json:"prompt" required:"true" description:"Prompt text for the job request."`
	Topic      string            `json:"topic,omitempty" default:"job.default" description:"Job topic to publish into the scheduler."`
	Priority   string            `json:"priority,omitempty" default:"normal" enum:"low,normal,high,critical" description:"Requested priority class for execution."`
	Capability string            `json:"capability,omitempty" description:"Safety capability label for policy evaluation."`
	RiskTags   []string          `json:"risk_tags,omitempty" description:"Safety risk tags attached to the request."`
	Labels     map[string]string `json:"labels,omitempty" description:"Additional labels for policy and routing controls."`
	MemoryID   string            `json:"memory_id,omitempty" description:"Optional memory ID to bind context retrieval."`
	PackID     string            `json:"pack_id,omitempty" description:"Optional pack ID context for the job."`
}

type cancelJobArgs struct {
	JobID  string `json:"job_id" required:"true" description:"Target job ID to cancel."`
	Reason string `json:"reason,omitempty" description:"Optional cancellation reason for audit context."`
}

type triggerWorkflowArgs struct {
	WorkflowID     string         `json:"workflow_id" required:"true" description:"Workflow ID to run."`
	Input          map[string]any `json:"input,omitempty" description:"Workflow input object validated by workflow schema."`
	DryRun         bool           `json:"dry_run,omitempty" default:"false" description:"When true, execute workflow in dry-run simulation mode."`
	IdempotencyKey string         `json:"idempotency_key,omitempty" description:"Optional idempotency key for run creation."`
}

type approveJobArgs struct {
	JobID string `json:"job_id" required:"true" description:"Job ID currently waiting for approval."`
	Note  string `json:"note,omitempty" description:"Optional approval note for audit trail."`
}

type rejectJobArgs struct {
	JobID  string `json:"job_id" required:"true" description:"Job ID currently waiting for approval."`
	Reason string `json:"reason" required:"true" description:"Required rejection reason."`
}

type queryPolicyArgs struct {
	Topic      string            `json:"topic" required:"true" description:"Topic to evaluate against the safety policy."`
	Priority   string            `json:"priority,omitempty" default:"normal" enum:"low,normal,high,critical" description:"Requested priority class for simulation."`
	Capability string            `json:"capability,omitempty" description:"Capability label sent in policy metadata."`
	RiskTags   []string          `json:"risk_tags,omitempty" description:"Risk tags sent to policy metadata."`
	Labels     map[string]string `json:"labels,omitempty" description:"Additional policy labels (includes MCP labels)." `
}

// RegisterAllTools registers all core Cordum MCP tools.
func RegisterAllTools(registry *ToolRegistry, bridge ServiceBridge) error {
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if bridge == nil {
		return fmt.Errorf("service bridge is nil")
	}

	specs := []struct {
		tool    Tool
		handler ToolHandler
	}{
		{
			tool: Tool{
				Name:        ToolSubmitJob,
				Description: "Submit a new job to Cordum for agent execution.",
				InputSchema: jsonSchema(submitJobArgs{}),
			},
			handler: submitJobHandler(bridge),
		},
		{
			tool: Tool{
				Name:        ToolCancelJob,
				Description: "Cancel a running or pending job.",
				InputSchema: jsonSchema(cancelJobArgs{}),
			},
			handler: cancelJobHandler(bridge),
		},
		{
			tool: Tool{
				Name:        ToolTriggerWorkflow,
				Description: "Start a workflow run with input parameters.",
				InputSchema: jsonSchema(triggerWorkflowArgs{}),
			},
			handler: triggerWorkflowHandler(bridge),
		},
		{
			tool: Tool{
				Name:        ToolApproveJob,
				Description: "Approve a job that requires human approval before execution.",
				InputSchema: jsonSchema(approveJobArgs{}),
			},
			handler: approveJobHandler(bridge),
		},
		{
			tool: Tool{
				Name:        ToolRejectJob,
				Description: "Reject a job that requires human approval before execution.",
				InputSchema: jsonSchema(rejectJobArgs{}),
			},
			handler: rejectJobHandler(bridge),
		},
		{
			tool: Tool{
				Name:        ToolQueryPolicy,
				Description: "Simulate policy evaluation without submitting a job.",
				InputSchema: jsonSchema(queryPolicyArgs{}),
			},
			handler: queryPolicyHandler(bridge),
		},
	}

	for _, spec := range specs {
		if err := registry.Register(spec.tool, spec.handler); err != nil {
			return err
		}
	}
	return nil
}

func submitJobHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args submitJobArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		args.Prompt = strings.TrimSpace(args.Prompt)
		if args.Prompt == "" {
			return nil, fmt.Errorf("%w: prompt is required", ErrInvalidParams)
		}
		args.Topic = strings.TrimSpace(args.Topic)
		if args.Topic == "" {
			args.Topic = "job.default"
		}
		args.Priority = strings.ToLower(strings.TrimSpace(args.Priority))
		if args.Priority == "" {
			args.Priority = "normal"
		}

		out, err := bridge.SubmitJob(ctx, SubmitJobInput{
			Prompt:     args.Prompt,
			Topic:      args.Topic,
			Priority:   args.Priority,
			Capability: strings.TrimSpace(args.Capability),
			RiskTags:   append([]string{}, args.RiskTags...),
			Labels:     cloneStringMap(args.Labels),
			MemoryID:   strings.TrimSpace(args.MemoryID),
			PackID:     strings.TrimSpace(args.PackID),
		})
		if err != nil {
			return mapSubmitJobError(err), nil
		}
		result := map[string]any{
			"job_id":   out.JobID,
			"trace_id": out.TraceID,
			"status":   "pending",
		}
		return toolSuccessResult("job submitted", result), nil
	}
}

func cancelJobHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args cancelJobArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		args.JobID = strings.TrimSpace(args.JobID)
		if args.JobID == "" {
			return nil, fmt.Errorf("%w: job_id is required", ErrInvalidParams)
		}
		if err := bridge.CancelJob(ctx, args.JobID, strings.TrimSpace(args.Reason)); err != nil {
			return mapCancelJobError(err, args.JobID), nil
		}
		return toolSuccessResult("job cancelled", map[string]any{
			"cancelled": true,
			"job_id":    args.JobID,
		}), nil
	}
}

func triggerWorkflowHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args triggerWorkflowArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		args.WorkflowID = strings.TrimSpace(args.WorkflowID)
		if args.WorkflowID == "" {
			return nil, fmt.Errorf("%w: workflow_id is required", ErrInvalidParams)
		}
		out, err := bridge.TriggerWorkflow(ctx, TriggerWorkflowInput{
			WorkflowID:     args.WorkflowID,
			Input:          cloneAnyMap(args.Input),
			DryRun:         args.DryRun,
			IdempotencyKey: strings.TrimSpace(args.IdempotencyKey),
		})
		if err != nil {
			return mapTriggerWorkflowError(err, args.WorkflowID), nil
		}
		return toolSuccessResult("workflow triggered", map[string]any{
			"run_id":      out.RunID,
			"workflow_id": out.WorkflowID,
			"status":      "pending",
		}), nil
	}
}

func approveJobHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args approveJobArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		args.JobID = strings.TrimSpace(args.JobID)
		if args.JobID == "" {
			return nil, fmt.Errorf("%w: job_id is required", ErrInvalidParams)
		}
		if err := bridge.ApproveJob(ctx, args.JobID, strings.TrimSpace(args.Note)); err != nil {
			return mapApprovalError(err, args.JobID, true), nil
		}
		return toolSuccessResult("job approved", map[string]any{
			"approved": true,
			"job_id":   args.JobID,
		}), nil
	}
}

func rejectJobHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args rejectJobArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		args.JobID = strings.TrimSpace(args.JobID)
		args.Reason = strings.TrimSpace(args.Reason)
		if args.JobID == "" {
			return nil, fmt.Errorf("%w: job_id is required", ErrInvalidParams)
		}
		if args.Reason == "" {
			return nil, fmt.Errorf("%w: reason is required", ErrInvalidParams)
		}
		if err := bridge.RejectJob(ctx, args.JobID, args.Reason); err != nil {
			return mapApprovalError(err, args.JobID, false), nil
		}
		return toolSuccessResult("job rejected", map[string]any{
			"rejected": true,
			"job_id":   args.JobID,
		}), nil
	}
}

func queryPolicyHandler(bridge ServiceBridge) ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		var args queryPolicyArgs
		if err := decodeToolArgs(params, &args); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		args.Topic = strings.TrimSpace(args.Topic)
		if args.Topic == "" {
			return nil, fmt.Errorf("%w: topic is required", ErrInvalidParams)
		}
		args.Priority = strings.ToLower(strings.TrimSpace(args.Priority))
		if args.Priority == "" {
			args.Priority = "normal"
		}

		out, err := bridge.SimulatePolicy(ctx, PolicySimInput{
			Topic:      args.Topic,
			Priority:   args.Priority,
			Capability: strings.TrimSpace(args.Capability),
			RiskTags:   append([]string{}, args.RiskTags...),
			Labels:     cloneStringMap(args.Labels),
		})
		if err != nil {
			return mapQueryPolicyError(err), nil
		}
		result := map[string]any{
			"decision":     normalizePolicyDecision(out.Decision),
			"reason":       out.Reason,
			"rule_id":      out.RuleID,
			"constraints":  out.Constraints,
			"remediations": out.Remediations,
		}
		return toolSuccessResult("policy simulated", result), nil
	}
}

func mapSubmitJobError(err error) *ToolCallResult {
	var be *BridgeError
	if errors.As(err, &be) {
		switch be.StatusCode {
		case http.StatusConflict:
			details := map[string]any{}
			if m, ok := be.Details.(map[string]any); ok {
				if jobID := strings.TrimSpace(asString(m["job_id"])); jobID != "" {
					details["existing_job_id"] = jobID
				}
			}
			return toolErrorResult("idempotency_conflict", "idempotency conflict", http.StatusConflict, details)
		case http.StatusTooManyRequests:
			return toolErrorResult("system_at_capacity", "system at capacity", http.StatusTooManyRequests, nil)
		}
		return toolErrorResult(be.Code, nonEmpty(be.Message, "submit job failed"), be.StatusCode, be.Details)
	}
	return toolErrorResult("submit_failed", err.Error(), 0, nil)
}

func mapCancelJobError(err error, jobID string) *ToolCallResult {
	var be *BridgeError
	if errors.As(err, &be) {
		switch be.StatusCode {
		case http.StatusNotFound:
			return toolErrorResult("job_not_found", "job not found", http.StatusNotFound, map[string]any{"job_id": jobID})
		case http.StatusConflict:
			return toolErrorResult("job_already_completed", "job already completed", http.StatusConflict, map[string]any{"job_id": jobID})
		}
		return toolErrorResult(be.Code, nonEmpty(be.Message, "cancel job failed"), be.StatusCode, be.Details)
	}
	return toolErrorResult("cancel_failed", err.Error(), 0, nil)
}

func mapTriggerWorkflowError(err error, workflowID string) *ToolCallResult {
	var be *BridgeError
	if errors.As(err, &be) {
		switch be.StatusCode {
		case http.StatusNotFound:
			return toolErrorResult("workflow_not_found", "workflow not found", http.StatusNotFound, map[string]any{"workflow_id": workflowID})
		case http.StatusUnprocessableEntity:
			return toolErrorResult("input_validation_failed", "input validation failed", http.StatusUnprocessableEntity, be.Details)
		}
		return toolErrorResult(be.Code, nonEmpty(be.Message, "trigger workflow failed"), be.StatusCode, be.Details)
	}
	return toolErrorResult("trigger_failed", err.Error(), 0, nil)
}

func mapApprovalError(err error, jobID string, approve bool) *ToolCallResult {
	action := "approve"
	if !approve {
		action = "reject"
	}
	var be *BridgeError
	if errors.As(err, &be) {
		switch be.StatusCode {
		case http.StatusNotFound:
			return toolErrorResult("job_not_found", "job not found", http.StatusNotFound, map[string]any{"job_id": jobID})
		case http.StatusConflict:
			code := "job_not_in_approval_state"
			msg := strings.ToLower(strings.TrimSpace(be.Message))
			if strings.Contains(msg, "policy snapshot changed") || strings.Contains(msg, "policy changed") {
				code = "policy_changed_since_request"
			}
			return toolErrorResult(code, nonEmpty(be.Message, "job not in approval state"), http.StatusConflict, map[string]any{"job_id": jobID})
		}
		return toolErrorResult(be.Code, nonEmpty(be.Message, action+" job failed"), be.StatusCode, be.Details)
	}
	return toolErrorResult(action+"_failed", err.Error(), 0, nil)
}

func mapQueryPolicyError(err error) *ToolCallResult {
	var be *BridgeError
	if errors.As(err, &be) {
		return toolErrorResult(be.Code, nonEmpty(be.Message, "policy simulation failed"), be.StatusCode, be.Details)
	}
	return toolErrorResult("policy_query_failed", err.Error(), 0, nil)
}

func toolSuccessResult(message string, structured any) *ToolCallResult {
	return &ToolCallResult{
		Content: []ContentItem{
			{Type: "text", Text: strings.TrimSpace(message)},
		},
		StructuredContent: structured,
	}
}

func toolErrorResult(code, message string, status int, details any) *ToolCallResult {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "tool_error"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "tool call failed"
	}
	payload := map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	if status > 0 {
		payload["error"].(map[string]any)["status"] = status
	}
	if details != nil {
		payload["error"].(map[string]any)["details"] = details
	}
	return &ToolCallResult{
		IsError: true,
		Content: []ContentItem{
			{Type: "text", Text: message},
		},
		StructuredContent: payload,
	}
}

func decodeToolArgs(params json.RawMessage, out any) error {
	if out == nil {
		return fmt.Errorf("output required")
	}
	raw := params
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	return nil
}

func nonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(fallback)
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// jsonSchema generates a basic JSON schema map from struct tags.
func jsonSchema(v any) map[string]any {
	typ := reflect.TypeOf(v)
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil || typ.Kind() != reflect.Struct {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}

	props := map[string]any{}
	required := make([]string, 0)

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name, omit := jsonFieldName(field)
		if omit || name == "" {
			continue
		}
		prop := schemaForType(field.Type)
		if desc := strings.TrimSpace(field.Tag.Get("description")); desc != "" {
			prop["description"] = desc
		}
		if def := strings.TrimSpace(field.Tag.Get("default")); def != "" {
			prop["default"] = parseDefaultValue(def, field.Type)
		}
		if enumTag := strings.TrimSpace(field.Tag.Get("enum")); enumTag != "" {
			items := strings.Split(enumTag, ",")
			enums := make([]any, 0, len(items))
			for _, item := range items {
				item = strings.TrimSpace(item)
				if item == "" {
					continue
				}
				enums = append(enums, parseDefaultValue(item, field.Type))
			}
			if len(enums) > 0 {
				prop["enum"] = enums
			}
		}
		props[name] = prop
		if isRequiredField(field) {
			required = append(required, name)
		}
	}

	out := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           props,
	}
	if len(required) > 0 {
		sortStrings(required)
		items := make([]any, 0, len(required))
		for _, item := range required {
			items = append(items, item)
		}
		out["required"] = items
	}
	return out
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", true
	}
	if tag == "" {
		return field.Name, false
	}
	parts := strings.Split(tag, ",")
	if len(parts) == 0 {
		return field.Name, false
	}
	name := strings.TrimSpace(parts[0])
	if name == "" {
		name = field.Name
	}
	return name, false
}

func isRequiredField(field reflect.StructField) bool {
	if strings.EqualFold(strings.TrimSpace(field.Tag.Get("required")), "true") {
		return true
	}
	tag := field.Tag.Get("json")
	if tag == "" {
		return false
	}
	for _, part := range strings.Split(tag, ",") {
		if strings.TrimSpace(part) == "omitempty" {
			return false
		}
	}
	return false
}

func schemaForType(typ reflect.Type) map[string]any {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		itemSchema := schemaForType(typ.Elem())
		return map[string]any{
			"type":  "array",
			"items": itemSchema,
		}
	case reflect.Map:
		prop := map[string]any{"type": "object"}
		if typ.Key().Kind() == reflect.String {
			elem := typ.Elem()
			for elem.Kind() == reflect.Pointer {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Interface {
				prop["additionalProperties"] = true
			} else {
				prop["additionalProperties"] = schemaForType(typ.Elem())
			}
		} else {
			prop["additionalProperties"] = true
		}
		return prop
	case reflect.Interface:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		}
	case reflect.Struct:
		props := map[string]any{}
		required := make([]any, 0)
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name, omit := jsonFieldName(field)
			if omit || name == "" {
				continue
			}
			props[name] = schemaForType(field.Type)
			if isRequiredField(field) {
				required = append(required, name)
			}
		}
		out := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           props,
		}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	default:
		return map[string]any{"type": "string"}
	}
}

func parseDefaultValue(raw string, typ reflect.Type) any {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return false
		}
		return v
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return int64(0)
		}
		return v
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return uint64(0)
		}
		return v
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return float64(0)
		}
		return v
	default:
		return raw
	}
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	for i := 0; i < len(values)-1; i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func normalizePolicyDecision(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "allow", "decision_type_allow", "decision-type-allow":
		return "allow"
	case "deny", "decision_type_deny", "decision-type-deny":
		return "deny"
	case "throttle", "decision_type_throttle", "decision-type-throttle":
		return "throttle"
	case "require_approval", "require_human", "decision_type_require_human", "decision-type-require-human":
		return "require_approval"
	}
	switch {
	case strings.Contains(normalized, "allow"):
		return "allow"
	case strings.Contains(normalized, "deny"):
		return "deny"
	case strings.Contains(normalized, "throttle"):
		return "throttle"
	case strings.Contains(normalized, "require"):
		return "require_approval"
	default:
		return normalized
	}
}

func toGatewayPriority(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low", "batch":
		return "batch"
	case "critical":
		return "critical"
	case "high", "normal", "interactive", "":
		return "interactive"
	default:
		return "interactive"
	}
}
