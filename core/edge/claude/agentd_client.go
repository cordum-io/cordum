package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AgentdClient is the hook-side client boundary to local cordum-agentd.
type AgentdClient interface {
	EvaluateHook(context.Context, AgentdRequest) (AgentdDecision, error)
}

// AgentdRequest is sent only to the local agentd. RawPayload is bounded by the
// runner and must never be logged or persisted by the hook.
//
// EDGE-016 added the trailing block of mapped/redacted/hashed fields so
// cordum-agentd can call Gateway evaluate without re-classifying — the hook
// already did the work via core/edge/claude.MapHookInput. Older agentd
// builds that don't know about these fields will silently ignore them via
// JSON optional decoding.
type AgentdRequest struct {
	EventName       string         `json:"event_name"`
	SessionID       string         `json:"session_id,omitempty"`
	ExecutionID     string         `json:"execution_id,omitempty"`
	TranscriptPath  string         `json:"transcript_path,omitempty"`
	CWD             string         `json:"cwd,omitempty"`
	PermissionMode  string         `json:"permission_mode,omitempty"`
	ToolName        string         `json:"tool_name,omitempty"`
	ToolUseID       string         `json:"tool_use_id,omitempty"`
	DurationMS      int            `json:"duration_ms,omitempty"`
	Prompt          string         `json:"prompt,omitempty"`
	Source          string         `json:"source,omitempty"`
	FilePath        string         `json:"file_path,omitempty"`
	FileEvent       string         `json:"file_event,omitempty"`
	ToolInput       map[string]any `json:"tool_input,omitempty"`
	ToolResponse    map[string]any `json:"tool_response,omitempty"`
	RawPayload      []byte         `json:"raw_payload,omitempty"`
	HookCommandArgs []string       `json:"hook_command_args,omitempty"`

	// EDGE-016 mapped/redacted/hashed fields. All optional so older agentd
	// builds keep working; presence indicates the hook ran the mapper.
	Layer         string            `json:"edge_layer,omitempty"`
	Kind          string            `json:"edge_kind,omitempty"`
	TenantID      string            `json:"tenant_id,omitempty"`
	PrincipalID   string            `json:"principal_id,omitempty"`
	Capability    string            `json:"capability,omitempty"`
	RiskTags      []string          `json:"risk_tags,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	InputRedacted map[string]any    `json:"input_redacted,omitempty"`
	InputHash     string            `json:"input_hash,omitempty"`
	ActionHash    string            `json:"action_hash,omitempty"`
	ReasonCode    string            `json:"reason_code,omitempty"`
}

type Decision string

const (
	DecisionAllow           Decision = "allow"
	DecisionDeny            Decision = "deny"
	DecisionAsk             Decision = "ask"
	DecisionRequireApproval Decision = "require_approval"
)

// AgentdDecision is the local agentd policy decision shape consumed by the
// hook. It is intentionally small; richer mapping belongs to later tasks.
type AgentdDecision struct {
	Decision          Decision       `json:"decision"`
	Reason            string         `json:"reason,omitempty"`
	ApprovalRef       string         `json:"approval_ref,omitempty"`
	AdditionalContext string         `json:"additional_context,omitempty"`
	UpdatedInput      map[string]any `json:"updated_input,omitempty"`
}

type HTTPAgentdClient struct {
	endpoint  string
	hookNonce string
	client    *http.Client
}

func NewHTTPAgentdClient(rawURL string, timeout time.Duration) (*HTTPAgentdClient, error) {
	return NewHTTPAgentdClientWithNonce(rawURL, timeout, "")
}

func NewHTTPAgentdClientWithNonce(rawURL string, timeout time.Duration, hookNonce string) (*HTTPAgentdClient, error) {
	if strings.TrimSpace(rawURL) == "" {
		rawURL = "http://127.0.0.1:8765/v1/edge/hooks/claude"
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid agentd url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported agentd url scheme %q", u.Scheme)
	}
	if !isLoopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("agentd url must be loopback/local")
	}
	endpoint, hadURLNonce := stripAgentdURLNonce(u.String())
	nonce := strings.TrimSpace(hookNonce)
	if hadURLNonce && nonce == "" {
		return nil, errors.New("agentd nonce must be supplied via CORDUM_AGENTD_HOOK_NONCE, not embedded in CORDUM_AGENTD_URL")
	}
	if timeout <= 0 {
		timeout = DefaultHookTimeout
	}
	return &HTTPAgentdClient{endpoint: endpoint, hookNonce: nonce, client: &http.Client{Timeout: timeout}}, nil
}

func (c *HTTPAgentdClient) EvaluateHook(ctx context.Context, req AgentdRequest) (AgentdDecision, error) {
	if c == nil || c.client == nil || c.endpoint == "" {
		return AgentdDecision{}, errors.New("agentd client not configured")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return AgentdDecision{}, fmt.Errorf("marshal agentd request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return AgentdDecision{}, fmt.Errorf("create agentd request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.hookNonce != "" {
		httpReq.Header.Set("X-Cordum-Agentd-Nonce", c.hookNonce)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return AgentdDecision{}, err
	}
	defer func() {
		// Drain any remaining body bytes so the underlying connection can be
		// reused and the keep-alive pool stays healthy when an oversize body
		// gets truncated by io.LimitReader below.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return AgentdDecision{}, fmt.Errorf("agentd status %d", resp.StatusCode)
	}
	var decision AgentdDecision
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := dec.Decode(&decision); err != nil {
		return AgentdDecision{}, fmt.Errorf("decode agentd decision: %w", err)
	}
	// Reject decisions outside the documented enum. A 200 response with
	// decision="unexpected" or "" would otherwise decode successfully and
	// fall through hookOutputForRun to an empty output — effectively
	// allowing the action. Surface the error so the caller's fail-closed
	// branch handles it instead.
	switch decision.Decision {
	case DecisionAllow, DecisionDeny, DecisionAsk, DecisionRequireApproval:
		return decision, nil
	default:
		return AgentdDecision{}, fmt.Errorf("agentd returned unknown decision: %q", decision.Decision)
	}
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
