package agentd

import (
	"context"
	"net/http"
)

// EvaluateRequest is the agentd-side bound, redacted input to Gateway
// /api/v1/edge/evaluate. It mirrors the Gateway edgeEvaluateRequest fields the
// agent actually sends and intentionally OMITS raw tool_input/raw_input/
// transcript fields — those are rejected by the Gateway and must never leave
// the local hook process. EDGE-016 already mapped/redacted/hashed the Claude
// payload before it reached agentd via claude.AgentdRequest.
type EvaluateRequest struct {
	EventID     string `json:"event_id,omitempty"`
	TenantID    string `json:"tenant_id"`
	PrincipalID string `json:"principal_id"`

	SessionID   string `json:"session_id"`
	ExecutionID string `json:"execution_id"`

	AgentProduct string `json:"agent_product,omitempty"`
	Layer        string `json:"layer,omitempty"`
	Kind         string `json:"kind,omitempty"`
	ToolName     string `json:"tool_name,omitempty"`
	ToolUseID    string `json:"tool_use_id,omitempty"`

	InputRedacted map[string]any `json:"input_redacted,omitempty"`
	InputHash     string         `json:"input_hash,omitempty"`

	ActionName string            `json:"action_name,omitempty"`
	Capability string            `json:"capability,omitempty"`
	RiskTags   []string          `json:"risk_tags,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`

	CWD       string `json:"cwd,omitempty"`
	Repo      string `json:"repo,omitempty"`
	GitRemote string `json:"git_remote,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	GitSHA    string `json:"git_sha,omitempty"`

	// Retry / inline-wait coordinates from EDGE-012. Default callers leave them
	// zero-valued. ApprovalRef triggers the Gateway consume-once CAS path; the
	// server-side cap on ApprovalWaitTimeoutMS is enforced regardless.
	ApprovalRef           string `json:"approval_ref,omitempty"`
	WaitForApproval       bool   `json:"wait_for_approval,omitempty"`
	ApprovalWaitTimeoutMS int    `json:"approval_wait_timeout_ms,omitempty"`
}

// EvaluateResponse mirrors the Gateway edgeEvaluateResponse. Decision values
// follow the canonical edgecore.EdgeDecision wire enum (ALLOW, DENY,
// REQUIRE_APPROVAL, THROTTLE, CONSTRAIN, RECORDED).
type EvaluateResponse struct {
	Decision                 string `json:"decision"`
	Reason                   string `json:"reason,omitempty"`
	RuleID                   string `json:"rule_id,omitempty"`
	RuleTier                 string `json:"rule_tier,omitempty"`
	PolicySnapshot           string `json:"policy_snapshot,omitempty"`
	WorkflowOverrideSnapshot string `json:"workflow_override_snapshot,omitempty"`
	JobOverrideSnapshot      string `json:"job_override_snapshot,omitempty"`
	ApprovalRef              string `json:"approval_ref,omitempty"`
	ApprovalURL              string `json:"approval_url,omitempty"`
	ActionHash               string `json:"action_hash,omitempty"`
	InputHash                string `json:"input_hash,omitempty"`
	CacheEligible            bool   `json:"cache_eligible,omitempty"`

	Constraints  map[string]any `json:"constraints,omitempty"`
	UpdatedInput map[string]any `json:"updated_input,omitempty"`
	EventID      string         `json:"event_id,omitempty"`

	Degraded     bool   `json:"degraded,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`

	PermissionDecision       string `json:"permission_decision,omitempty"`
	PermissionDecisionReason string `json:"permission_decision_reason,omitempty"`
	ExitCode                 int    `json:"exit_code,omitempty"`

	TerminalTitle   string `json:"terminal_title,omitempty"`
	TerminalMessage string `json:"terminal_message,omitempty"`
	WaitStrategy    string `json:"wait_strategy,omitempty"`
	WaitAfter       string `json:"wait_after,omitempty"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
}

// EvaluateClient is the narrow boundary the agentd evaluator uses to talk to
// Gateway. It exists so tests can stub the HTTP call without touching net/http.
type EvaluateClient interface {
	Evaluate(ctx context.Context, req EvaluateRequest) (*EvaluateResponse, error)
}

// Evaluate POSTs the bounded redacted action to /api/v1/edge/evaluate and
// returns the parsed decision. Errors flow through GatewayClient.doJSON so
// timeouts surface as ErrGatewayTimeout, transport failures preserve the
// underlying error category, and Gateway error bodies pass through the same
// secret-redaction pass as the lifecycle calls.
func (c *GatewayClient) Evaluate(ctx context.Context, req EvaluateRequest) (*EvaluateResponse, error) {
	bounded := boundedEvaluateRequest(req)
	var out EvaluateResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/edge/evaluate", bounded, &out); err != nil {
		return nil, err
	}
	if err := validateEvaluateResponse(out); err != nil {
		return nil, err
	}
	return &out, nil
}

// boundedEvaluateRequest applies MaxGatewayMetadataValueBytes to every
// agent-supplied string and copies labels/risk_tags into bounded slices/maps.
// The bound is defensive — the EDGE-016 hook mapper already produces redacted
// fields, but this guarantees agentd never relays a giant string to Gateway,
// which would otherwise be rejected with 413 only after a wasted round-trip.
func boundedEvaluateRequest(req EvaluateRequest) EvaluateRequest {
	req.EventID = boundMetadataString(req.EventID)
	req.TenantID = boundMetadataString(req.TenantID)
	req.PrincipalID = boundMetadataString(req.PrincipalID)
	req.SessionID = boundMetadataString(req.SessionID)
	req.ExecutionID = boundMetadataString(req.ExecutionID)
	req.AgentProduct = boundMetadataString(req.AgentProduct)
	req.Layer = boundMetadataString(req.Layer)
	req.Kind = boundMetadataString(req.Kind)
	req.ToolName = boundMetadataString(req.ToolName)
	req.ToolUseID = boundMetadataString(req.ToolUseID)
	req.InputHash = boundMetadataString(req.InputHash)
	req.ActionName = boundMetadataString(req.ActionName)
	req.Capability = boundMetadataString(req.Capability)
	req.CWD = boundMetadataString(req.CWD)
	req.Repo = boundMetadataString(req.Repo)
	req.GitRemote = boundMetadataString(req.GitRemote)
	req.GitBranch = boundMetadataString(req.GitBranch)
	req.GitSHA = boundMetadataString(req.GitSHA)
	req.ApprovalRef = boundMetadataString(req.ApprovalRef)

	if len(req.RiskTags) > 0 {
		tags := make([]string, len(req.RiskTags))
		for i, t := range req.RiskTags {
			tags[i] = boundMetadataString(t)
		}
		req.RiskTags = tags
	}
	if len(req.Labels) > 0 {
		labels := make(map[string]string, len(req.Labels))
		for k, v := range req.Labels {
			labels[boundMetadataString(k)] = boundMetadataString(v)
		}
		req.Labels = labels
	}
	return req
}
