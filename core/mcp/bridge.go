package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultGatewayAddr = "http://localhost:8081"
)

var (
	// ErrBridgeUnavailable indicates that no backend bridge is configured.
	ErrBridgeUnavailable = errors.New("mcp service bridge unavailable")
)

// BridgeError carries HTTP/service error context from bridge implementations.
type BridgeError struct {
	StatusCode int
	Code       string
	Message    string
	Details    any
}

func (e *BridgeError) Error() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "service bridge error"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("bridge error (%d): %s", e.StatusCode, msg)
	}
	return msg
}

// NewBridgeError creates a typed bridge error.
func NewBridgeError(status int, code, message string, details any) *BridgeError {
	return &BridgeError{
		StatusCode: status,
		Code:       strings.TrimSpace(code),
		Message:    strings.TrimSpace(message),
		Details:    details,
	}
}

// NewBridgeErrorFromHTTP builds a BridgeError from an HTTP status and body.
func NewBridgeErrorFromHTTP(status int, body []byte) *BridgeError {
	payload := map[string]any{}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}
	msg := strings.TrimSpace(string(body))
	if raw := strings.TrimSpace(asString(payload["error"])); raw != "" {
		msg = raw
	}
	code := strings.TrimSpace(asString(payload["code"]))
	if code == "" {
		switch status {
		case http.StatusBadRequest:
			code = "invalid_request"
		case http.StatusUnauthorized:
			code = "unauthorized"
		case http.StatusForbidden:
			code = "forbidden"
		case http.StatusNotFound:
			code = "not_found"
		case http.StatusConflict:
			code = "conflict"
		case http.StatusTooManyRequests:
			code = "rate_limited"
		case http.StatusServiceUnavailable:
			code = "service_unavailable"
		default:
			if status >= 500 {
				code = "upstream_error"
			} else {
				code = "request_failed"
			}
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &BridgeError{
		StatusCode: status,
		Code:       code,
		Message:    msg,
		Details:    payload,
	}
}

// HTTPServiceBridgeConfig configures the HTTP-backed bridge.
type HTTPServiceBridgeConfig struct {
	BaseURL    string
	TenantID   string
	HTTPClient *http.Client
	// AllowedHosts is an optional host/domain allowlist for outbound gateway calls.
	AllowedHosts []string
	// AllowPrivateHosts permits loopback/private/link-local hosts when true.
	// Keep false unless private routing is explicitly required.
	AllowPrivateHosts bool
	apiKey            string
}

// WithAuthToken sets the bearer/API token used for outbound gateway calls.
func (c HTTPServiceBridgeConfig) WithAuthToken(token string) HTTPServiceBridgeConfig {
	c.apiKey = strings.TrimSpace(token)
	return c
}

// WithOutboundSigner installs a signer that will stamp every outgoing
// request with ECDSA P-256 headers. Nil signer is accepted (noop).
// Returns b for chaining.
func (b *HTTPServiceBridge) WithOutboundSigner(signer outboundSigner, agentID string) *HTTPServiceBridge {
	if b == nil {
		return nil
	}
	b.outboundSigner = signer
	b.outboundAgentID = strings.TrimSpace(agentID)
	return b
}

// OutboundSignAuditHook is the bridge-level audit callback fired after
// every successfully-signed outbound request. The hook receives the
// target URL path, method, and the 6-header map the signer produced.
// Keeping it a callback avoids an import of core/audit inside core/mcp
// (which would cycle back through the gateway's auditExporter).
type OutboundSignAuditHook func(method, path, keyID, nonce, tenant, agentID string)

// WithOutboundSignAuditHook installs the audit hook. Nil is accepted
// — unhooked deployments simply skip SIEM emission.
func (b *HTTPServiceBridge) WithOutboundSignAuditHook(hook OutboundSignAuditHook) *HTTPServiceBridge {
	if b == nil {
		return nil
	}
	b.outboundSignHook = hook
	return b
}

// OutboundInvocationAuditor brackets every outbound HTTP request with
// terminal audit emission. Start is called BEFORE client.Do, Finish is
// called AFTER — so Finish sees the real response status, latency, and
// any transport error. Unlike OutboundSignAuditHook (which fires
// per-sign and can't carry the terminal result), this is the contract
// the DoD's "mcp.tool_outbound_invocation" event needs.
//
// Keeping the interface callback-shaped rather than importing
// ToolInvocationAuditor directly avoids tangling every bridge consumer
// with the concrete audit implementation — the gateway wires a real
// auditor adapter; tests wire a recording double.
type OutboundInvocationAuditor interface {
	StartRequest(ctx context.Context, method, path string, body []byte) OutboundRequestHandle
	FinishRequest(h OutboundRequestHandle, statusCode int, responseBody []byte, err error)
}

// OutboundRequestHandle is the opaque per-request token the auditor
// returns from StartRequest and reads back in FinishRequest.
type OutboundRequestHandle interface{}

// WithOutboundInvocationAuditor installs the terminal-response audit
// hook. Nil is accepted — the bridge then falls back to the
// sign-time-only OutboundSignAuditHook (if wired) for attempt
// coverage. Concrete callers (cordum-mcp stdio + gateway test
// harness) wire an adapter that emits mcp.tool_outbound_invocation on
// FinishRequest with the real status/latency/body-redacted payload.
func (b *HTTPServiceBridge) WithOutboundInvocationAuditor(a OutboundInvocationAuditor) *HTTPServiceBridge {
	if b == nil {
		return nil
	}
	b.outboundInvocationAuditor = a
	return b
}

// HTTPServiceBridge maps ServiceBridge methods to gateway HTTP APIs.
type HTTPServiceBridge struct {
	baseURL           string
	apiKey            string
	tenantID          string
	allowedHosts      []string
	allowPrivateHosts bool
	httpClient        *http.Client

	// outboundSigner is applied to every outgoing request when non-nil,
	// attaching the X-Cordum-{Signature,Timestamp,Nonce,KeyId,Tenant,AgentId}
	// headers. See core/mcp/outbound. Wire via WithOutboundSigner.
	outboundSigner outboundSigner

	// outboundAgentID is stamped into the signed-request Agent-Id
	// header. Defaults to the tenantID when empty.
	outboundAgentID string

	// outboundSignHook is invoked after each successful sign so the
	// caller can emit an audit event. Nil is a noop.
	outboundSignHook OutboundSignAuditHook

	// outboundInvocationAuditor brackets every outbound request with
	// Start/Finish so terminal status + latency + redacted body land
	// on a mcp.tool_outbound_invocation SIEMEvent. Nil = no audit.
	outboundInvocationAuditor OutboundInvocationAuditor
}

// OutboundSigner is the narrow interface the bridge needs from the
// core/mcp/outbound.Signer. Declaring it here avoids an import cycle
// (outbound already imports crypto; keeping the bridge free of crypto
// types lets outbound unit-test without Go pulling mcp back in).
//
// Exported so sibling packages (core/mcp/tools, core/mcp/resources)
// can forward a signer through their GatewayClient builders without
// duplicating the type. Any *outbound.Signer satisfies this interface.
type OutboundSigner interface {
	SignRequest(method string, params []byte, tenant, agentID string) (map[string]string, error)
}

// outboundSigner kept as an unexported alias for backwards compat
// with internal callers — safe to remove once every caller migrates
// to the exported name.
type outboundSigner = OutboundSigner

// NewHTTPServiceBridge creates an HTTP bridge with secure defaults.
func NewHTTPServiceBridge(cfg HTTPServiceBridgeConfig) *HTTPServiceBridge {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultGatewayAddr
	}
	tenantID := strings.TrimSpace(cfg.TenantID)
	if tenantID == "" {
		tenantID = strings.TrimSpace(os.Getenv("CORDUM_TENANT_ID"))
	}
	if tenantID == "" {
		tenantID = "default"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = SafeHTTPClient(10 * time.Second)
	}
	return &HTTPServiceBridge{
		baseURL:           strings.TrimRight(baseURL, "/"),
		apiKey:            strings.TrimSpace(cfg.apiKey),
		tenantID:          tenantID,
		allowedHosts:      normalizeAllowedHosts(cfg.AllowedHosts),
		allowPrivateHosts: cfg.AllowPrivateHosts,
		httpClient:        httpClient,
	}
}

func (b *HTTPServiceBridge) SubmitJob(ctx context.Context, req SubmitJobInput) (*SubmitJobOutput, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	body := map[string]any{
		"prompt":   req.Prompt,
		"topic":    req.Topic,
		"priority": toGatewayPriority(req.Priority),
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

	var out SubmitJobOutput
	if err := b.doJSON(ctx, http.MethodPost, "/api/v1/jobs", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (b *HTTPServiceBridge) CancelJob(ctx context.Context, jobID string, reason string) error {
	if b == nil {
		return ErrBridgeUnavailable
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return NewBridgeError(http.StatusBadRequest, "invalid_request", "job_id is required", nil)
	}

	var body any
	if strings.TrimSpace(reason) != "" {
		body = map[string]any{"reason": strings.TrimSpace(reason)}
	}
	var out map[string]any
	if err := b.doJSON(ctx, http.MethodPost, "/api/v1/jobs/"+url.PathEscape(jobID)+"/cancel", nil, body, &out); err != nil {
		return err
	}
	if state := strings.TrimSpace(asString(out["state"])); state != "" && !strings.EqualFold(state, "cancelled") {
		return NewBridgeError(http.StatusConflict, "job_already_completed", "job already completed", out)
	}
	return nil
}

func (b *HTTPServiceBridge) TriggerWorkflow(ctx context.Context, req TriggerWorkflowInput) (*TriggerOutput, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	workflowID := strings.TrimSpace(req.WorkflowID)
	if workflowID == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "workflow_id is required", nil)
	}
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/runs"
	if req.DryRun {
		path += "?dry_run=true"
	}
	headers := map[string]string{}
	if v := strings.TrimSpace(req.IdempotencyKey); v != "" {
		headers["Idempotency-Key"] = v
	}
	payload := req.Input
	if payload == nil {
		payload = map[string]any{}
	}

	var out struct {
		RunID string `json:"run_id"`
	}
	if err := b.doJSON(ctx, http.MethodPost, path, headers, payload, &out); err != nil {
		var be *BridgeError
		if errors.As(err, &be) && be.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(be.Message), "input schema validation failed") {
			return nil, NewBridgeError(http.StatusUnprocessableEntity, "input_validation_failed", be.Message, be.Details)
		}
		return nil, err
	}
	return &TriggerOutput{RunID: out.RunID, WorkflowID: workflowID}, nil
}

func (b *HTTPServiceBridge) ApproveJob(ctx context.Context, jobID string, note string) error {
	if b == nil {
		return ErrBridgeUnavailable
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return NewBridgeError(http.StatusBadRequest, "invalid_request", "job_id is required", nil)
	}
	var body any
	if strings.TrimSpace(note) != "" {
		body = map[string]any{"note": strings.TrimSpace(note)}
	}
	return b.doJSON(ctx, http.MethodPost, "/api/v1/approvals/"+url.PathEscape(jobID)+"/approve", nil, body, nil)
}

func (b *HTTPServiceBridge) RejectJob(ctx context.Context, jobID string, reason string) error {
	if b == nil {
		return ErrBridgeUnavailable
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return NewBridgeError(http.StatusBadRequest, "invalid_request", "job_id is required", nil)
	}
	body := map[string]any{}
	if strings.TrimSpace(reason) != "" {
		body["reason"] = strings.TrimSpace(reason)
	}
	return b.doJSON(ctx, http.MethodPost, "/api/v1/approvals/"+url.PathEscape(jobID)+"/reject", nil, body, nil)
}

func (b *HTTPServiceBridge) SimulatePolicy(ctx context.Context, req PolicySimInput) (*PolicySimOutput, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	body := map[string]any{
		"topic":    req.Topic,
		"tenant":   b.tenantID,
		"org_id":   b.tenantID,
		"priority": toGatewayPriority(req.Priority),
		"meta": map[string]any{
			"tenant_id":  b.tenantID,
			"capability": strings.TrimSpace(req.Capability),
			"risk_tags":  append([]string{}, req.RiskTags...),
			"labels":     req.Labels,
		},
	}
	if len(req.Labels) > 0 {
		body["labels"] = req.Labels
	}

	var raw map[string]any
	if err := b.doJSON(ctx, http.MethodPost, "/api/v1/policy/simulate", nil, body, &raw); err != nil {
		return nil, err
	}
	out := &PolicySimOutput{
		Decision:    normalizePolicyDecision(asString(raw["decision"])),
		Reason:      firstString(raw, "reason"),
		RuleID:      firstString(raw, "ruleId", "rule_id"),
		Constraints: asMap(raw["constraints"]),
		Remediations: asSliceMapAny(
			raw["remediations"],
		),
	}
	if out.Constraints == nil {
		out.Constraints = map[string]any{}
	}
	if out.Remediations == nil {
		out.Remediations = []map[string]any{}
	}
	return out, nil
}

func (b *HTTPServiceBridge) doJSON(ctx context.Context, method, path string, headers map[string]string, body any, out any) error {
	status, payload, err := b.doRequest(ctx, method, path, headers, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return NewBridgeErrorFromHTTP(status, payload)
	}
	if out == nil {
		return nil
	}
	if len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (b *HTTPServiceBridge) doRequest(ctx context.Context, method, path string, headers map[string]string, body any) (int, []byte, error) {
	if b == nil {
		return 0, nil, ErrBridgeUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var payload io.Reader
	var payloadBytes []byte
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return 0, nil, fmt.Errorf("encode request: %w", err)
		}
		payloadBytes = buf.Bytes()
		payload = buf
	}

	// #nosec G704 -- target URL is constrained by bridge configuration and validated below.
	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, payload)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %w", err)
	}
	pinnedIPs, err := validateAndResolveOutboundURL(req.Context(), req.URL, b.allowedHosts, b.allowPrivateHosts)
	if err != nil {
		return 0, nil, fmt.Errorf("validate request target: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.apiKey != "" {
		req.Header.Set("X-API-Key", b.apiKey)
	}
	if b.tenantID != "" {
		req.Header.Set("X-Tenant-ID", b.tenantID)
	}
	// Outbound signature (task-ba236f62). Signs method + sha256(body)
	// + fresh nonce + timestamp + tenant + agent_id. Server-side
	// middleware verifies via core/mcp/outbound.Verifier. Fails open
	// on signer error (log + continue) so a transient signing fault
	// doesn't break the API request — the audit chain records the
	// unsigned call so operators can detect the regression.
	if b.outboundSigner != nil {
		bodyBytes := []byte{}
		if body != nil {
			// We already marshalled into payload above but don't have
			// the bytes directly — re-encode once here. Acceptable cost
			// given signing is per-request and the body is small.
			if buf, ok := payload.(*bytes.Buffer); ok {
				bodyBytes = buf.Bytes()
			}
		}
		agentID := b.outboundAgentID
		if agentID == "" {
			agentID = b.tenantID
		}
		if signed, err := b.outboundSigner.SignRequest(method+" "+path, bodyBytes, b.tenantID, agentID); err == nil {
			for k, v := range signed {
				if v != "" {
					req.Header.Set(k, v)
				}
			}
			// Epic rail "All MCP tool invocations must produce audit
			// events" — fire the audit hook just after the signer
			// succeeds, BEFORE the request actually goes out. Emitting
			// the event per-sign (rather than per-response) captures
			// the attempt even when the remote server is unreachable,
			// which is exactly the case an operator most wants logged.
			if b.outboundSignHook != nil {
				b.outboundSignHook(method, path, signed["X-Cordum-Key-Id"], signed["X-Cordum-Nonce"], signed["X-Cordum-Tenant"], signed["X-Cordum-Agent-Id"])
			}
		} else {
			// Fail-CLOSED on signer error (QA reopen fix). The earlier
			// fail-open behaviour let an attacker who could make the
			// signer transiently error strip signatures from outbound
			// calls. Returning the error here means a misbehaving
			// signer surfaces as a hard request failure — operators
			// notice immediately instead of auditors finding a gap in
			// the chain weeks later.
			return 0, nil, fmt.Errorf("outbound signer failed: %w", err)
		}
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	client := b.httpClient
	if client == nil {
		client = SafeHTTPClient(10 * time.Second)
	}
	// Pin DNS resolution to prevent rebinding attacks (TOCTOU).
	if len(pinnedIPs) > 0 {
		client = &http.Client{
			Timeout:       client.Timeout,
			CheckRedirect: client.CheckRedirect,
			Transport:     &http.Transport{DialContext: pinnedDialer(pinnedIPs)},
		}
	}
	// Start the invocation audit BEFORE client.Do so latency is measured
	// across the full transport. FinishRequest always fires (defer) so
	// both the success path and every error path (DNS failure, TLS
	// failure, body-read failure) produce a terminal SIEMEvent — the
	// DoD requires "every outbound call produces an audit event", which
	// must include transport-level failures that previously slipped
	// through the per-sign hook.
	var handle OutboundRequestHandle
	if b.outboundInvocationAuditor != nil {
		handle = b.outboundInvocationAuditor.StartRequest(ctx, method, path, payloadBytes)
	}
	status := 0
	var data []byte
	var callErr error
	defer func() {
		if b.outboundInvocationAuditor != nil {
			b.outboundInvocationAuditor.FinishRequest(handle, status, data, callErr)
		}
	}()

	// #nosec G704 -- URL is validated and DNS-pinned via validateAndResolveOutboundURL above.
	resp, err := client.Do(req)
	if err != nil {
		callErr = fmt.Errorf("request failed: %w", err)
		return 0, nil, callErr
	}
	defer func() { _ = resp.Body.Close() }()

	data, err = io.ReadAll(resp.Body)
	if err != nil {
		callErr = fmt.Errorf("read response body: %w", err)
		return 0, nil, callErr
	}
	status = resp.StatusCode
	return status, data, nil
}

// DirectServiceBridgeConfig allows callers to bind direct in-process handlers.
type DirectServiceBridgeConfig struct {
	SubmitJobFunc       func(ctx context.Context, req SubmitJobInput) (*SubmitJobOutput, error)
	CancelJobFunc       func(ctx context.Context, jobID string, reason string) error
	TriggerWorkflowFunc func(ctx context.Context, req TriggerWorkflowInput) (*TriggerOutput, error)
	ApproveJobFunc      func(ctx context.Context, jobID string, note string) error
	RejectJobFunc       func(ctx context.Context, jobID string, reason string) error
	SimulatePolicyFunc  func(ctx context.Context, req PolicySimInput) (*PolicySimOutput, error)

	// Mutating hooks — kept nil by default so an in-process bridge
	// configured for read-only paths returns ErrBridgeUnavailable on
	// a mutating call instead of silently no-opping.
	CreateWorkflowFunc      func(ctx context.Context, req CreateWorkflowInput) (*CreateWorkflowOutput, error)
	InstallPackFunc         func(ctx context.Context, req InstallPackInput) (*InstallPackOutput, error)
	UninstallPackFunc       func(ctx context.Context, req UninstallPackInput) error
	RegisterAgentFunc       func(ctx context.Context, req RegisterAgentInput) (*RegisterAgentOutput, error)
	UpdatePolicyBundleFunc  func(ctx context.Context, req UpdatePolicyBundleInput) (*UpdatePolicyBundleOutput, error)
	RevokeWorkerSessionFunc func(ctx context.Context, req RevokeWorkerSessionInput) error
	SetAgentScopeFunc       func(ctx context.Context, req SetAgentScopeInput) (*SetAgentScopeOutput, error)
}

// DirectServiceBridge is an in-process ServiceBridge based on function hooks.
type DirectServiceBridge struct {
	submitJob       func(ctx context.Context, req SubmitJobInput) (*SubmitJobOutput, error)
	cancelJob       func(ctx context.Context, jobID string, reason string) error
	triggerWorkflow func(ctx context.Context, req TriggerWorkflowInput) (*TriggerOutput, error)
	approveJob      func(ctx context.Context, jobID string, note string) error
	rejectJob       func(ctx context.Context, jobID string, reason string) error
	simulatePolicy  func(ctx context.Context, req PolicySimInput) (*PolicySimOutput, error)

	// Mutating hooks
	createWorkflow      func(ctx context.Context, req CreateWorkflowInput) (*CreateWorkflowOutput, error)
	installPack         func(ctx context.Context, req InstallPackInput) (*InstallPackOutput, error)
	uninstallPack       func(ctx context.Context, req UninstallPackInput) error
	registerAgent       func(ctx context.Context, req RegisterAgentInput) (*RegisterAgentOutput, error)
	updatePolicyBundle  func(ctx context.Context, req UpdatePolicyBundleInput) (*UpdatePolicyBundleOutput, error)
	revokeWorkerSession func(ctx context.Context, req RevokeWorkerSessionInput) error
	setAgentScope       func(ctx context.Context, req SetAgentScopeInput) (*SetAgentScopeOutput, error)
}

// NewDirectServiceBridge creates a direct bridge.
func NewDirectServiceBridge(cfg DirectServiceBridgeConfig) *DirectServiceBridge {
	return &DirectServiceBridge{
		submitJob:           cfg.SubmitJobFunc,
		cancelJob:           cfg.CancelJobFunc,
		triggerWorkflow:     cfg.TriggerWorkflowFunc,
		approveJob:          cfg.ApproveJobFunc,
		rejectJob:           cfg.RejectJobFunc,
		simulatePolicy:      cfg.SimulatePolicyFunc,
		createWorkflow:      cfg.CreateWorkflowFunc,
		installPack:         cfg.InstallPackFunc,
		uninstallPack:       cfg.UninstallPackFunc,
		registerAgent:       cfg.RegisterAgentFunc,
		updatePolicyBundle:  cfg.UpdatePolicyBundleFunc,
		revokeWorkerSession: cfg.RevokeWorkerSessionFunc,
		setAgentScope:       cfg.SetAgentScopeFunc,
	}
}

func (b *DirectServiceBridge) SubmitJob(ctx context.Context, req SubmitJobInput) (*SubmitJobOutput, error) {
	if b == nil || b.submitJob == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.submitJob(ctx, req)
}

func (b *DirectServiceBridge) CancelJob(ctx context.Context, jobID string, reason string) error {
	if b == nil || b.cancelJob == nil {
		return ErrBridgeUnavailable
	}
	return b.cancelJob(ctx, jobID, reason)
}

func (b *DirectServiceBridge) TriggerWorkflow(ctx context.Context, req TriggerWorkflowInput) (*TriggerOutput, error) {
	if b == nil || b.triggerWorkflow == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.triggerWorkflow(ctx, req)
}

func (b *DirectServiceBridge) ApproveJob(ctx context.Context, jobID string, note string) error {
	if b == nil || b.approveJob == nil {
		return ErrBridgeUnavailable
	}
	return b.approveJob(ctx, jobID, note)
}

func (b *DirectServiceBridge) RejectJob(ctx context.Context, jobID string, reason string) error {
	if b == nil || b.rejectJob == nil {
		return ErrBridgeUnavailable
	}
	return b.rejectJob(ctx, jobID, reason)
}

func (b *DirectServiceBridge) SimulatePolicy(ctx context.Context, req PolicySimInput) (*PolicySimOutput, error) {
	if b == nil || b.simulatePolicy == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.simulatePolicy(ctx, req)
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func asMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func asSliceMapAny(value any) []map[string]any {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if val := strings.TrimSpace(asString(raw[key])); val != "" {
			return val
		}
	}
	return ""
}
