package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/cordum/cordum/core/mcp"
)

func (s *server) newMCPServiceBridge() mcp.ServiceBridge {
	if s == nil {
		return mcp.NewDirectServiceBridge(mcp.DirectServiceBridgeConfig{})
	}
	return mcp.NewDirectServiceBridge(mcp.DirectServiceBridgeConfig{
		SubmitJobFunc: func(ctx context.Context, req mcp.SubmitJobInput) (*mcp.SubmitJobOutput, error) {
			body := map[string]any{
				"prompt":   req.Prompt,
				"topic":    req.Topic,
				"priority": req.Priority,
			}
			if strings.TrimSpace(req.Capability) != "" {
				body["capability"] = strings.TrimSpace(req.Capability)
			}
			if len(req.RiskTags) > 0 {
				body["risk_tags"] = append([]string{}, req.RiskTags...)
			}
			if len(req.Labels) > 0 {
				body["labels"] = req.Labels
			}
			if strings.TrimSpace(req.MemoryID) != "" {
				body["memory_id"] = strings.TrimSpace(req.MemoryID)
			}
			if strings.TrimSpace(req.PackID) != "" {
				body["pack_id"] = strings.TrimSpace(req.PackID)
			}

			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodPost, "/api/v1/jobs", nil, nil, body, s.handleSubmitJobHTTP)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			jobID := strings.TrimSpace(mcpBridgeString(payload["job_id"]))
			if jobID == "" {
				return nil, mcp.NewBridgeError(http.StatusBadGateway, "invalid_response", "missing job_id in submit response", payload)
			}
			return &mcp.SubmitJobOutput{
				JobID:   jobID,
				TraceID: strings.TrimSpace(mcpBridgeString(payload["trace_id"])),
			}, nil
		},
		CancelJobFunc: func(ctx context.Context, jobID string, reason string) error {
			body := map[string]any{}
			if strings.TrimSpace(reason) != "" {
				body["reason"] = strings.TrimSpace(reason)
			}
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				"/api/v1/jobs/"+jobID+"/cancel",
				nil,
				map[string]string{"id": jobID},
				body,
				s.handleCancelJob,
			)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			if state := strings.TrimSpace(mcpBridgeString(payload["state"])); state != "" && !strings.EqualFold(state, "cancelled") {
				return mcp.NewBridgeError(http.StatusConflict, "job_already_completed", "job already completed", payload)
			}
			return nil
		},
		TriggerWorkflowFunc: func(ctx context.Context, req mcp.TriggerWorkflowInput) (*mcp.TriggerOutput, error) {
			target := "/api/v1/workflows/" + req.WorkflowID + "/runs"
			if req.DryRun {
				target += "?dry_run=true"
			}
			headers := map[string]string{}
			if strings.TrimSpace(req.IdempotencyKey) != "" {
				headers["Idempotency-Key"] = strings.TrimSpace(req.IdempotencyKey)
			}
			input := req.Input
			if input == nil {
				input = map[string]any{}
			}
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				target,
				headers,
				map[string]string{"id": req.WorkflowID},
				input,
				s.handleStartRun,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				be := mcp.NewBridgeErrorFromHTTP(status, raw)
				if status == http.StatusBadRequest && strings.Contains(strings.ToLower(be.Message), "input schema validation failed") {
					return nil, mcp.NewBridgeError(http.StatusUnprocessableEntity, "input_validation_failed", be.Message, be.Details)
				}
				return nil, be
			}
			runID := strings.TrimSpace(mcpBridgeString(payload["run_id"]))
			if runID == "" {
				return nil, mcp.NewBridgeError(http.StatusBadGateway, "invalid_response", "missing run_id in workflow response", payload)
			}
			return &mcp.TriggerOutput{
				RunID:      runID,
				WorkflowID: strings.TrimSpace(req.WorkflowID),
			}, nil
		},
		ApproveJobFunc: func(ctx context.Context, jobID string, note string) error {
			body := map[string]any{}
			if strings.TrimSpace(note) != "" {
				body["note"] = strings.TrimSpace(note)
			}
			status, _, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				"/api/v1/approvals/"+jobID+"/approve",
				nil,
				map[string]string{"job_id": jobID},
				body,
				s.handleApproveJob,
			)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			return nil
		},
		RejectJobFunc: func(ctx context.Context, jobID string, reason string) error {
			body := map[string]any{"reason": strings.TrimSpace(reason)}
			status, _, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				"/api/v1/approvals/"+jobID+"/reject",
				nil,
				map[string]string{"job_id": jobID},
				body,
				s.handleRejectJob,
			)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			return nil
		},
		SimulatePolicyFunc: func(ctx context.Context, req mcp.PolicySimInput) (*mcp.PolicySimOutput, error) {
			tenantID := s.mcpTenantFromContext(ctx)
			body := map[string]any{
				"topic":    req.Topic,
				"tenant":   tenantID,
				"org_id":   tenantID,
				"priority": req.Priority,
				"meta": map[string]any{
					"tenant_id":  tenantID,
					"capability": strings.TrimSpace(req.Capability),
					"risk_tags":  append([]string{}, req.RiskTags...),
					"labels":     req.Labels,
				},
			}
			if len(req.Labels) > 0 {
				body["labels"] = req.Labels
			}
			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodPost, "/api/v1/policy/simulate", nil, nil, body, s.handlePolicySimulate)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			out := &mcp.PolicySimOutput{
				Decision: strings.TrimSpace(mcpBridgeString(payload["decision"])),
				Reason:   strings.TrimSpace(mcpBridgeString(payload["reason"])),
				RuleID:   strings.TrimSpace(mcpBridgeFirstString(payload, "ruleId", "rule_id")),
			}
			if constraints, ok := payload["constraints"].(map[string]any); ok {
				out.Constraints = constraints
			} else {
				out.Constraints = map[string]any{}
			}
			if rems, ok := payload["remediations"].([]any); ok {
				out.Remediations = make([]map[string]any, 0, len(rems))
				for _, item := range rems {
					if m, ok := item.(map[string]any); ok {
						out.Remediations = append(out.Remediations, m)
					}
				}
			} else {
				out.Remediations = []map[string]any{}
			}
			return out, nil
		},
	})
}

func (s *server) invokeMCPJSONHandler(
	ctx context.Context,
	method string,
	target string,
	headers map[string]string,
	pathValues map[string]string,
	body any,
	handler http.HandlerFunc,
) (int, map[string]any, []byte, error) {
	if handler == nil {
		return 0, nil, nil, fmt.Errorf("handler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("encode body: %w", err)
		}
		payload = encoded
	}

	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req = req.WithContext(ctx)
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("X-Tenant-ID") == "" {
		req.Header.Set("X-Tenant-ID", s.mcpTenantFromContext(ctx))
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	for key, value := range pathValues {
		req.SetPathValue(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	rr := httptest.NewRecorder()
	handler(rr, req)

	raw := rr.Body.Bytes()
	decoded := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return rr.Code, decoded, raw, nil
}

func (s *server) invokeMCPAnyHandler(
	ctx context.Context,
	method string,
	target string,
	headers map[string]string,
	pathValues map[string]string,
	body any,
	handler http.HandlerFunc,
) (int, any, []byte, error) {
	if handler == nil {
		return 0, nil, nil, fmt.Errorf("handler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("encode body: %w", err)
		}
		payload = encoded
	}

	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req = req.WithContext(ctx)
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("X-Tenant-ID") == "" {
		req.Header.Set("X-Tenant-ID", s.mcpTenantFromContext(ctx))
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	for key, value := range pathValues {
		req.SetPathValue(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	rr := httptest.NewRecorder()
	handler(rr, req)

	raw := rr.Body.Bytes()
	var decoded any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return rr.Code, decoded, raw, nil
}

func (s *server) mcpTenantFromContext(ctx context.Context) string {
	if auth := authFromContext(ctx); auth != nil {
		if tenant := strings.TrimSpace(auth.Tenant); tenant != "" {
			return tenant
		}
	}
	if tenant := strings.TrimSpace(s.tenant); tenant != "" {
		return tenant
	}
	return "default"
}

func mcpBridgeString(v any) string {
	if v == nil {
		return ""
	}
	switch typed := v.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", v)
	}
}

func mcpBridgeFirstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if val := strings.TrimSpace(mcpBridgeString(data[key])); val != "" {
			return val
		}
	}
	return ""
}
