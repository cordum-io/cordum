package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Read-only ServiceBridge methods for the MCP discovery surface
// (task-466b6a6a). Every method maps 1:1 to an existing gateway REST
// endpoint so the MCP layer does not own any new business logic.
//
// All methods run under the caller's tenant/principal context already
// carried on the HTTP request — there is no elevation, there is no
// write. Implementations here just call the gateway over HTTP and
// reshape the response into a ListPage or ResourceItem.
//
// The DirectServiceBridge in bridge.go is a test-only bridge that
// bypasses HTTP; its read-only methods return ErrReadOnlyUnsupported
// because building an in-process index of every read endpoint is out
// of scope for the local harness.

// ErrReadOnlyUnsupported signals that a ServiceBridge implementation
// does not provide the read-only surface (e.g. DirectServiceBridge).
// Wrapped with BridgeError in gateway paths so the MCP layer returns a
// descriptive JSON-RPC error code.
var ErrReadOnlyUnsupported = errors.New("mcp: read-only bridge surface not supported by this transport")

// getJSON fetches a gateway JSON endpoint under the bridge's auth and
// decodes the response. 4xx/5xx are mapped to BridgeError. Routes through
// the same validateAndResolveOutboundURL + pinnedDialer guard used by
// doRequest so every read-only path (jobs/runs/workflows/packs/topics/
// workers/agents/approvals/audit-query/audit-verify/status) is protected
// against DNS-rebinding TOCTOU on b.baseURL — pre-fix the GET surface
// skipped both the host allowlist and IP pinning (HIGH audit finding
// 2026-05-20).
func (b *HTTPServiceBridge) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	u := b.baseURL + path
	if len(query) > 0 {
		if strings.Contains(u, "?") {
			u = u + "&" + query.Encode()
		} else {
			u = u + "?" + query.Encode()
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	pinnedIPs, err := validateAndResolveOutboundURL(req.Context(), req.URL, b.allowedHosts, b.allowPrivateHosts)
	if err != nil {
		return fmt.Errorf("validate request target: %w", err)
	}
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	if b.tenantID != "" {
		req.Header.Set("X-Tenant-ID", b.tenantID)
	}
	req.Header.Set("Accept", "application/json")

	client := b.httpClient
	if client == nil {
		client = SafeHTTPClient(10 * time.Second)
	}
	// Pin DNS resolution to the IPs validateAndResolveOutboundURL
	// returned so a hostile resolver cannot flip the target to a
	// private address between validation and dial. Mirror doRequest's
	// transport build so behaviour stays uniform across GET/POST.
	if len(pinnedIPs) > 0 {
		client = &http.Client{
			Timeout:       client.Timeout,
			CheckRedirect: client.CheckRedirect,
			Transport:     &http.Transport{DialContext: pinnedDialer(pinnedIPs)},
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http get %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return NewBridgeError(resp.StatusCode, "", path, nil)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// listQuery builds the query-string for list endpoints from a ListInput.
// Keeps the filter semantics in one place so every list method hands
// the gateway the same parameters.
func listQuery(req ListInput) url.Values {
	q := url.Values{}
	if req.Cursor != "" {
		q.Set("cursor", req.Cursor)
	}
	if req.PageSize > 0 {
		q.Set("limit", strconv.Itoa(req.PageSize))
	}
	for k, v := range req.Filter {
		q.Set(k, v)
	}
	return q
}

// itemsFromResponse extracts a ListPage from a gateway response that
// follows the `{items: [...], next_cursor?: string, total?: int}` shape.
// Gateway responses vary slightly (some use `next_offset` instead of
// `next_cursor`); coerce to one envelope here.
func itemsFromResponse(raw map[string]any) *ListPage {
	page := &ListPage{Items: []map[string]any{}}
	if items, ok := raw["items"].([]any); ok {
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				page.Items = append(page.Items, m)
			}
		}
	}
	page.NextCursor = strings.TrimSpace(asString(raw["next_cursor"]))
	if total, ok := raw["total"]; ok {
		switch v := total.(type) {
		case float64:
			page.Total = int(v)
		case int:
			page.Total = v
		case int64:
			page.Total = int(v)
		}
	}
	return page
}

// ListJobs — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListJobs(ctx context.Context, req ListInput) (*ListPage, error) {
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/jobs", listQuery(req), &raw); err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

// GetJob — HTTP bridge implementation.
func (b *HTTPServiceBridge) GetJob(ctx context.Context, jobID string) (*ResourceItem, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("%w: job_id required", ErrInvalidParams)
	}
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/jobs/"+url.PathEscape(jobID), nil, &raw); err != nil {
		return nil, err
	}
	return &ResourceItem{ID: jobID, Kind: "job", Data: raw}, nil
}

// ListRuns — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListRuns(ctx context.Context, req ListInput) (*ListPage, error) {
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/runs", listQuery(req), &raw); err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

// GetRun — HTTP bridge implementation.
func (b *HTTPServiceBridge) GetRun(ctx context.Context, runID string) (*ResourceItem, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("%w: run_id required", ErrInvalidParams)
	}
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/runs/"+url.PathEscape(runID), nil, &raw); err != nil {
		return nil, err
	}
	return &ResourceItem{ID: runID, Kind: "run", Data: raw}, nil
}

// GetRunTimeline — HTTP bridge implementation.
func (b *HTTPServiceBridge) GetRunTimeline(ctx context.Context, runID string) (*ResourceItem, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("%w: run_id required", ErrInvalidParams)
	}
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/runs/"+url.PathEscape(runID)+"/timeline", nil, &raw); err != nil {
		return nil, err
	}
	return &ResourceItem{ID: runID, Kind: "run_timeline", Data: raw}, nil
}

// ListWorkflows — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListWorkflows(ctx context.Context, req ListInput) (*ListPage, error) {
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/workflows", listQuery(req), &raw); err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

// ListPacks — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListPacks(ctx context.Context, req ListInput) (*ListPage, error) {
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/packs", listQuery(req), &raw); err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

// ListTopics — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListTopics(ctx context.Context, req ListInput) (*ListPage, error) {
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/topics", listQuery(req), &raw); err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

// ListWorkers — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListWorkers(ctx context.Context, req ListInput) (*ListPage, error) {
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/workers", listQuery(req), &raw); err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

// ListAgents — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListAgents(ctx context.Context, req ListInput) (*ListPage, error) {
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/agents", listQuery(req), &raw); err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

// ListPendingApprovals — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListPendingApprovals(ctx context.Context, req ListInput) (*ListPage, error) {
	q := listQuery(req)
	if q.Get("status") == "" {
		q.Set("status", "pending")
	}
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/approvals", q, &raw); err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

// QueryAudit — HTTP bridge implementation.
func (b *HTTPServiceBridge) QueryAudit(ctx context.Context, req AuditQueryInput) (*ListPage, error) {
	q := listQuery(req.ListInput)
	if req.EventType != "" {
		q.Set("type", req.EventType)
	}
	if req.Since != "" {
		q.Set("since", req.Since)
	}
	if req.Until != "" {
		q.Set("until", req.Until)
	}
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/audit/query", q, &raw); err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

// VerifyAudit — HTTP bridge implementation.
func (b *HTTPServiceBridge) VerifyAudit(ctx context.Context, tenant string) (*ResourceItem, error) {
	q := url.Values{}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/audit/verify", q, &raw); err != nil {
		return nil, err
	}
	return &ResourceItem{Kind: "audit_verify", Data: raw}, nil
}

// GetStatus — HTTP bridge implementation.
func (b *HTTPServiceBridge) GetStatus(ctx context.Context) (*ResourceItem, error) {
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/status", nil, &raw); err != nil {
		return nil, err
	}
	return &ResourceItem{Kind: "status", Data: raw}, nil
}

// DirectServiceBridge read-only stubs. Returning ErrReadOnlyUnsupported
// wrapped with BridgeError(501) keeps the local test harness compiling
// without dragging every Redis store type into this file. Production
// paths never hit these; the stdio cordum-mcp binary uses HTTPServiceBridge.

func (b *DirectServiceBridge) ListJobs(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListJobs", nil)
}
func (b *DirectServiceBridge) GetJob(context.Context, string) (*ResourceItem, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "GetJob", nil)
}
func (b *DirectServiceBridge) ListRuns(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListRuns", nil)
}
func (b *DirectServiceBridge) GetRun(context.Context, string) (*ResourceItem, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "GetRun", nil)
}
func (b *DirectServiceBridge) GetRunTimeline(context.Context, string) (*ResourceItem, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "GetRunTimeline", nil)
}
func (b *DirectServiceBridge) ListWorkflows(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListWorkflows", nil)
}
func (b *DirectServiceBridge) ListPacks(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListPacks", nil)
}
func (b *DirectServiceBridge) ListTopics(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListTopics", nil)
}
func (b *DirectServiceBridge) ListWorkers(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListWorkers", nil)
}
func (b *DirectServiceBridge) ListAgents(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListAgents", nil)
}
func (b *DirectServiceBridge) ListPendingApprovals(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListPendingApprovals", nil)
}
func (b *DirectServiceBridge) QueryAudit(context.Context, AuditQueryInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "QueryAudit", nil)
}
func (b *DirectServiceBridge) VerifyAudit(context.Context, string) (*ResourceItem, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "VerifyAudit", nil)
}
func (b *DirectServiceBridge) GetStatus(context.Context) (*ResourceItem, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "GetStatus", nil)
}
