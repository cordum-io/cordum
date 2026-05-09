package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policy"
)

type policyEvaluateHTTPPayload struct {
	Rule        json.RawMessage            `json:"rule"`
	BundleID    string                     `json:"bundle_id"`
	Scope       *policy.RuleScope          `json:"scope"`
	JobContext  *jobEvaluationHTTPContext  `json:"job_context"`
	EdgeContext *edgeEvaluationHTTPContext `json:"edge_context"`
}

type jobEvaluationHTTPContext struct {
	TenantID    string                  `json:"tenant_id"`
	JobID       string                  `json:"job_id"`
	WorkflowID  string                  `json:"workflow_id"`
	Topic       string                  `json:"topic"`
	PrincipalID string                  `json:"principal_id"`
	Labels      map[string]string       `json:"labels"`
	MemoryID    string                  `json:"memory_id"`
	Capability  string                  `json:"capability"`
	RiskTags    []string                `json:"risk_tags"`
	Input       *jobEvaluationHTTPInput `json:"input"`
}

type jobEvaluationHTTPInput struct {
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type edgeEvaluationHTTPContext struct {
	TenantID          string            `json:"tenant_id"`
	PrincipalID       string            `json:"principal_id"`
	SessionID         string            `json:"session_id"`
	ExecutionID       string            `json:"execution_id"`
	AgentProduct      string            `json:"agent_product"`
	ToolName          string            `json:"tool_name"`
	ToolInputRedacted map[string]any    `json:"tool_input_redacted"`
	InputHash         string            `json:"input_hash"`
	ToolInputHash     string            `json:"tool_input_hash"`
	Labels            map[string]string `json:"labels"`
	RiskTags          []string          `json:"risk_tags"`
}

type policyRuleHTTP struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Type        string               `json:"type"`
	Scope       policy.RuleScope     `json:"scope"`
	Status      string               `json:"status"`
	Version     string               `json:"version"`
	Audit       policy.AuditMetadata `json:"audit"`
	Match       json.RawMessage      `json:"match"`
	Decide      json.RawMessage      `json:"decide"`
	Description string               `json:"description"`
}

func (s *server) handlePolicyEvaluateDispatch(w http.ResponseWriter, r *http.Request) {
	body, err := readPolicyEvaluateBody(w, r)
	if err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if !policyEvaluateBodyLooksUnified(body) {
		r.Body = io.NopCloser(bytes.NewReader(body))
		s.handlePolicyCheck(w, r, "evaluate")
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermPolicyWrite, "admin") {
		return
	}
	req, err := decodePolicyEvaluateHTTPRequest(body)
	if err != nil {
		writePolicyEvaluateHTTPError(w, err)
		return
	}
	if err := s.authorizePolicyEvaluateHTTPRequest(r, &req); err != nil {
		writeForbidden(w, r, err)
		return
	}
	result, err := s.evaluateUnifiedPolicy(r.Context(), req)
	if err != nil {
		writePolicyEvaluateHTTPError(w, err)
		return
	}
	writeJSON(w, policyEvaluateHTTPResponse{Decision: result.Decision})
}

type policyEvaluateHTTPResponse struct {
	Decision policy.Decision `json:"decision"`
}

func readPolicyEvaluateBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	limit := maxJSONBodyBytes()
	if requestLimit, ok := requestBodyLimitFromContext(r.Context()); ok && requestLimit > 0 {
		limit = requestLimit
	}
	reader := r.Body
	if limit > 0 {
		reader = http.MaxBytesReader(w, r.Body, limit)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, tierLimitFromMaxBytes(int64(maxErr.Limit))
		}
		return nil, err
	}
	return body, nil
}

func policyEvaluateBodyLooksUnified(body []byte) bool {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return false
	}
	for _, key := range []string{"rule", "bundle_id", "scope", "job_context", "edge_context"} {
		if _, ok := top[key]; ok {
			return true
		}
	}
	return false
}

func decodePolicyEvaluateHTTPRequest(body []byte) (policyEvaluationRequest, error) {
	var payload policyEvaluateHTTPPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return policyEvaluationRequest{}, newPolicyEvaluateError(policyEvaluateValidation, "invalid json", err)
	}
	rule, err := decodePolicyEvaluateHTTPRule(payload.Rule)
	if err != nil {
		return policyEvaluationRequest{}, err
	}
	return policyEvaluationRequest{Rule: rule, BundleID: payload.BundleID, Scope: payload.Scope, JobContext: payload.JobContext.toInternal(), EdgeContext: payload.EdgeContext.toInternal()}, nil
}

func decodePolicyEvaluateHTTPRule(raw json.RawMessage) (*policy.Rule, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var wire policyRuleHTTP
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, newPolicyEvaluateError(policyEvaluateValidation, "invalid rule", err)
	}
	return &policy.Rule{ID: strings.TrimSpace(wire.ID), Name: strings.TrimSpace(wire.Name), Type: policy.RuleType(strings.TrimSpace(wire.Type)), Scope: wire.Scope, Status: policy.RuleStatus(strings.TrimSpace(wire.Status)), Version: strings.TrimSpace(wire.Version), Audit: wire.Audit, Match: cloneRawMessage(wire.Match), Decide: cloneRawMessage(wire.Decide), Description: strings.TrimSpace(wire.Description)}, nil
}

func (c *jobEvaluationHTTPContext) toInternal() *jobEvaluationContext {
	if c == nil {
		return nil
	}
	out := &jobEvaluationContext{TenantID: c.TenantID, JobID: c.JobID, WorkflowID: c.WorkflowID, Topic: c.Topic, PrincipalID: c.PrincipalID, Labels: clonePolicyEvalStringMap(c.Labels), MemoryID: c.MemoryID, Capability: c.Capability, RiskTags: append([]string{}, c.RiskTags...)}
	if c.Input != nil {
		out.Input = jobEvaluationInput{Content: c.Input.Content, ContentType: c.Input.ContentType, SizeBytes: c.Input.SizeBytes}
	}
	return out
}

func (c *edgeEvaluationHTTPContext) toInternal() *edgeEvaluationContext {
	if c == nil {
		return nil
	}
	return &edgeEvaluationContext{TenantID: c.TenantID, PrincipalID: c.PrincipalID, SessionID: c.SessionID, ExecutionID: c.ExecutionID, AgentProduct: c.AgentProduct, ToolName: c.ToolName, ToolInputRedacted: clonePolicyEvalAnyMap(c.ToolInputRedacted), InputHash: c.InputHash, ToolInputHash: c.ToolInputHash, Labels: clonePolicyEvalStringMap(c.Labels), RiskTags: append([]string{}, c.RiskTags...)}
}

func (s *server) authorizePolicyEvaluateHTTPRequest(r *http.Request, req *policyEvaluationRequest) error {
	if req.JobContext != nil {
		tenant, err := s.resolveTenant(r, req.JobContext.TenantID)
		if err != nil {
			return err
		}
		principal, err := s.resolvePrincipal(r, req.JobContext.PrincipalID)
		if err != nil {
			return err
		}
		req.JobContext.TenantID = tenant
		req.JobContext.PrincipalID = principal
	}
	if req.EdgeContext != nil {
		tenant, err := s.resolveTenant(r, req.EdgeContext.TenantID)
		if err != nil {
			return err
		}
		principal, err := s.resolvePrincipal(r, req.EdgeContext.PrincipalID)
		if err != nil {
			return err
		}
		req.EdgeContext.TenantID = tenant
		req.EdgeContext.PrincipalID = principal
	}
	return nil
}

func writePolicyEvaluateHTTPError(w http.ResponseWriter, err error) {
	var evalErr *policyEvaluateError
	if errors.As(err, &evalErr) {
		writeErrorJSON(w, evalErr.StatusCode(), policyEvaluateErrorMessage(evalErr))
		return
	}
	writeErrorJSON(w, http.StatusInternalServerError, "internal error")
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}
