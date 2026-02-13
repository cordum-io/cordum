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

// HTTPServiceBridge maps ServiceBridge methods to gateway HTTP APIs.
type HTTPServiceBridge struct {
	baseURL           string
	apiKey            string
	tenantID          string
	allowedHosts      []string
	allowPrivateHosts bool
	httpClient        *http.Client
}

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
		httpClient = &http.Client{Timeout: 10 * time.Second}
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
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return 0, nil, fmt.Errorf("encode request: %w", err)
		}
		payload = buf
	}

	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, payload)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %w", err)
	}
	if err := validateOutboundTargetURL(req.Context(), req.URL, b.allowedHosts, b.allowPrivateHosts); err != nil {
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
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req) // #nosec G107 -- URL is validated via validateOutboundTargetURL above.
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, nil
}

// DirectServiceBridgeConfig allows callers to bind direct in-process handlers.
type DirectServiceBridgeConfig struct {
	SubmitJobFunc       func(ctx context.Context, req SubmitJobInput) (*SubmitJobOutput, error)
	CancelJobFunc       func(ctx context.Context, jobID string, reason string) error
	TriggerWorkflowFunc func(ctx context.Context, req TriggerWorkflowInput) (*TriggerOutput, error)
	ApproveJobFunc      func(ctx context.Context, jobID string, note string) error
	RejectJobFunc       func(ctx context.Context, jobID string, reason string) error
	SimulatePolicyFunc  func(ctx context.Context, req PolicySimInput) (*PolicySimOutput, error)
}

// DirectServiceBridge is an in-process ServiceBridge based on function hooks.
type DirectServiceBridge struct {
	submitJob       func(ctx context.Context, req SubmitJobInput) (*SubmitJobOutput, error)
	cancelJob       func(ctx context.Context, jobID string, reason string) error
	triggerWorkflow func(ctx context.Context, req TriggerWorkflowInput) (*TriggerOutput, error)
	approveJob      func(ctx context.Context, jobID string, note string) error
	rejectJob       func(ctx context.Context, jobID string, reason string) error
	simulatePolicy  func(ctx context.Context, req PolicySimInput) (*PolicySimOutput, error)
}

// NewDirectServiceBridge creates a direct bridge.
func NewDirectServiceBridge(cfg DirectServiceBridgeConfig) *DirectServiceBridge {
	return &DirectServiceBridge{
		submitJob:       cfg.SubmitJobFunc,
		cancelJob:       cfg.CancelJobFunc,
		triggerWorkflow: cfg.TriggerWorkflowFunc,
		approveJob:      cfg.ApproveJobFunc,
		rejectJob:       cfg.RejectJobFunc,
		simulatePolicy:  cfg.SimulatePolicyFunc,
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
