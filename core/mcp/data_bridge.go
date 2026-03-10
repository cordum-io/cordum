package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// HTTPDataBridgeConfig configures the HTTP-backed resource bridge.
type HTTPDataBridgeConfig struct {
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
func (c HTTPDataBridgeConfig) WithAuthToken(token string) HTTPDataBridgeConfig {
	c.apiKey = strings.TrimSpace(token)
	return c
}

// HTTPDataBridge maps DataBridge methods to gateway HTTP APIs.
type HTTPDataBridge struct {
	baseURL           string
	apiKey            string
	tenantID          string
	allowedHosts      []string
	allowPrivateHosts bool
	httpClient        *http.Client
}

// NewHTTPDataBridge creates an HTTP DataBridge with secure defaults.
func NewHTTPDataBridge(cfg HTTPDataBridgeConfig) *HTTPDataBridge {
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
	return &HTTPDataBridge{
		baseURL:           strings.TrimRight(baseURL, "/"),
		apiKey:            strings.TrimSpace(cfg.apiKey),
		tenantID:          tenantID,
		allowedHosts:      normalizeAllowedHosts(cfg.AllowedHosts),
		allowPrivateHosts: cfg.AllowPrivateHosts,
		httpClient:        httpClient,
	}
}

func (b *HTTPDataBridge) GetJob(ctx context.Context, id string) (*JobDetail, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "id is required", nil)
	}
	var payload map[string]any
	if err := b.doJSON(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(id), nil, nil, &payload); err != nil {
		return nil, err
	}
	job := JobDetail(payload)
	return &job, nil
}

func (b *HTTPDataBridge) ListJobs(ctx context.Context, opts JobListOpts) (*JobList, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	values := url.Values{}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	values.Set("limit", strconv.Itoa(limit))
	if status := strings.TrimSpace(opts.Status); status != "" {
		values.Set("state", status)
	}
	if opts.Cursor > 0 {
		values.Set("cursor", strconv.FormatInt(opts.Cursor, 10))
	}
	path := "/api/v1/jobs?" + values.Encode()

	var payload map[string]any
	if err := b.doJSON(ctx, http.MethodGet, path, nil, nil, &payload); err != nil {
		return nil, err
	}

	items := toMapSlice(payload["items"])
	out := &JobList{Items: items}
	if next, ok := toInt64(payload["next_cursor"]); ok {
		out.NextCursor = &next
	}
	return out, nil
}

func (b *HTTPDataBridge) ListWorkflowRuns(ctx context.Context, wfID string, limit int) (*RunList, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	wfID = strings.TrimSpace(wfID)
	if wfID == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "workflow_id is required", nil)
	}
	values := url.Values{}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	path := "/api/v1/workflows/" + url.PathEscape(wfID) + "/runs"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}

	var payload any
	if err := b.doJSON(ctx, http.MethodGet, path, nil, nil, &payload); err != nil {
		return nil, err
	}

	items := []map[string]any{}
	switch typed := payload.(type) {
	case []any:
		items = toMapSlice(typed)
	case map[string]any:
		items = toMapSlice(typed["items"])
	}
	return &RunList{
		WorkflowID: wfID,
		Items:      items,
	}, nil
}

func (b *HTTPDataBridge) GetWorkflowRun(ctx context.Context, wfID, runID string) (*RunDetail, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, NewBridgeError(http.StatusBadRequest, "invalid_request", "run_id is required", nil)
	}
	var payload map[string]any
	if err := b.doJSON(ctx, http.MethodGet, "/api/v1/workflow-runs/"+url.PathEscape(runID), nil, nil, &payload); err != nil {
		return nil, err
	}
	if expectedWF := strings.TrimSpace(wfID); expectedWF != "" {
		if actual := strings.TrimSpace(asString(payload["workflow_id"])); actual != "" && actual != expectedWF {
			return nil, NewBridgeError(http.StatusNotFound, "not_found", "workflow run not found", nil)
		}
	}
	run := RunDetail(payload)
	return &run, nil
}

func (b *HTTPDataBridge) ListAuditEntries(ctx context.Context, limit int) ([]AuditEntry, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	var payload map[string]any
	if err := b.doJSON(ctx, http.MethodGet, "/api/v1/policy/audit", nil, nil, &payload); err != nil {
		return nil, err
	}
	items := toMapSlice(payload["items"])
	if limit <= 0 {
		limit = 50
	}
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]AuditEntry, 0, len(items))
	for _, item := range items {
		out = append(out, AuditEntry(item))
	}
	return out, nil
}

func (b *HTTPDataBridge) GetSystemHealth(ctx context.Context) (*HealthStatus, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	var payload map[string]any
	if err := b.doJSON(ctx, http.MethodGet, "/api/v1/status", nil, nil, &payload); err != nil {
		return nil, err
	}
	health := HealthStatus(payload)
	return &health, nil
}

func (b *HTTPDataBridge) ListPolicies(ctx context.Context) (*PolicySummary, error) {
	if b == nil {
		return nil, ErrBridgeUnavailable
	}
	var bundlesPayload map[string]any
	if err := b.doJSON(ctx, http.MethodGet, "/api/v1/policy/bundles", nil, nil, &bundlesPayload); err != nil {
		return nil, err
	}
	items := toMapSlice(bundlesPayload["items"])

	currentSnapshot := ""
	var snapshotsPayload map[string]any
	if err := b.doJSON(ctx, http.MethodGet, "/api/v1/policy/snapshots", nil, nil, &snapshotsPayload); err == nil {
		if list, ok := snapshotsPayload["snapshots"].([]any); ok && len(list) > 0 {
			currentSnapshot = strings.TrimSpace(asString(list[0]))
		}
	}

	summary := PolicySummary{
		"active_bundles":      items,
		"current_snapshot_id": currentSnapshot,
		"safety_stance":       inferSafetyStance(items),
	}
	return &summary, nil
}

func (b *HTTPDataBridge) doJSON(ctx context.Context, method, path string, headers map[string]string, body any, out any) error {
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

func (b *HTTPDataBridge) doRequest(ctx context.Context, method, path string, headers map[string]string, body any) (int, []byte, error) {
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

	// #nosec G704 -- target URL is constrained by bridge configuration and validated below.
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
		client = SafeHTTPClient(10 * time.Second)
	}
	// #nosec G704 -- URL is validated via validateOutboundTargetURL above.
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp.StatusCode, data, nil
}

// DirectDataBridgeConfig allows callers to bind direct in-process resource reads.
type DirectDataBridgeConfig struct {
	GetJobFunc            func(ctx context.Context, id string) (*JobDetail, error)
	ListJobsFunc          func(ctx context.Context, opts JobListOpts) (*JobList, error)
	ListWorkflowRunsFunc  func(ctx context.Context, wfID string, limit int) (*RunList, error)
	GetWorkflowRunFunc    func(ctx context.Context, wfID, runID string) (*RunDetail, error)
	ListAuditEntriesFunc  func(ctx context.Context, limit int) ([]AuditEntry, error)
	GetSystemHealthFunc   func(ctx context.Context) (*HealthStatus, error)
	ListPoliciesSummaryFn func(ctx context.Context) (*PolicySummary, error)
}

// DirectDataBridge is an in-process DataBridge based on function hooks.
type DirectDataBridge struct {
	getJob           func(ctx context.Context, id string) (*JobDetail, error)
	listJobs         func(ctx context.Context, opts JobListOpts) (*JobList, error)
	listWorkflowRuns func(ctx context.Context, wfID string, limit int) (*RunList, error)
	getWorkflowRun   func(ctx context.Context, wfID, runID string) (*RunDetail, error)
	listAuditEntries func(ctx context.Context, limit int) ([]AuditEntry, error)
	getSystemHealth  func(ctx context.Context) (*HealthStatus, error)
	listPolicies     func(ctx context.Context) (*PolicySummary, error)
}

// NewDirectDataBridge creates a direct data bridge.
func NewDirectDataBridge(cfg DirectDataBridgeConfig) *DirectDataBridge {
	return &DirectDataBridge{
		getJob:           cfg.GetJobFunc,
		listJobs:         cfg.ListJobsFunc,
		listWorkflowRuns: cfg.ListWorkflowRunsFunc,
		getWorkflowRun:   cfg.GetWorkflowRunFunc,
		listAuditEntries: cfg.ListAuditEntriesFunc,
		getSystemHealth:  cfg.GetSystemHealthFunc,
		listPolicies:     cfg.ListPoliciesSummaryFn,
	}
}

func (b *DirectDataBridge) GetJob(ctx context.Context, id string) (*JobDetail, error) {
	if b == nil || b.getJob == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.getJob(ctx, id)
}

func (b *DirectDataBridge) ListJobs(ctx context.Context, opts JobListOpts) (*JobList, error) {
	if b == nil || b.listJobs == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.listJobs(ctx, opts)
}

func (b *DirectDataBridge) ListWorkflowRuns(ctx context.Context, wfID string, limit int) (*RunList, error) {
	if b == nil || b.listWorkflowRuns == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.listWorkflowRuns(ctx, wfID, limit)
}

func (b *DirectDataBridge) GetWorkflowRun(ctx context.Context, wfID, runID string) (*RunDetail, error) {
	if b == nil || b.getWorkflowRun == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.getWorkflowRun(ctx, wfID, runID)
}

func (b *DirectDataBridge) ListAuditEntries(ctx context.Context, limit int) ([]AuditEntry, error) {
	if b == nil || b.listAuditEntries == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.listAuditEntries(ctx, limit)
}

func (b *DirectDataBridge) GetSystemHealth(ctx context.Context) (*HealthStatus, error) {
	if b == nil || b.getSystemHealth == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.getSystemHealth(ctx)
}

func (b *DirectDataBridge) ListPolicies(ctx context.Context) (*PolicySummary, error) {
	if b == nil || b.listPolicies == nil {
		return nil, ErrBridgeUnavailable
	}
	return b.listPolicies(ctx)
}

func toMapSlice(raw any) []map[string]any {
	list, ok := raw.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func toInt64(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, true
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func inferSafetyStance(items []map[string]any) string {
	if len(items) == 0 {
		return "permissive"
	}
	enabledCount := 0
	var totalRules int64
	for _, item := range items {
		if enabled, ok := item["enabled"].(bool); ok && enabled {
			enabledCount++
		}
		if ruleCount, ok := toInt64(item["rule_count"]); ok {
			totalRules += ruleCount
		}
	}
	if enabledCount == 0 || totalRules == 0 {
		return "permissive"
	}
	if totalRules >= 20 {
		return "strict"
	}
	return "balanced"
}
