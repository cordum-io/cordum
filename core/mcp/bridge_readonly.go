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
// The DirectServiceBridge in bridge.go bypasses HTTP: the gateway's HTTP
// MCP runtime binds it to in-process handlers. GetJob and GetStatus call
// their own hooks, and nine more reads share the code below through the
// GetJSONFunc hook. ListPacks, ListWorkers and QueryAudit, and any read
// without its hook, return a 501 read_only_unsupported BridgeError.

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
		client = pinnedClient(client, pinnedIPs)
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

// jsonGetter fetches one gateway read route and decodes its JSON answer.
// HTTPServiceBridge passes its getJSON and DirectServiceBridge its
// GetJSONFunc hook, so both transports share the reads below.
type jsonGetter func(ctx context.Context, path string, query url.Values, out any) error

// readJSON fetches path and returns its answer as an object. A route that
// answers a bare JSON array (handleListWorkflows, handleGetRunTimeline), or
// null for an empty one, has it placed under arrayKey.
func readJSON(ctx context.Context, get jsonGetter, path string, q url.Values, arrayKey string) (map[string]any, error) {
	var raw any
	if err := get(ctx, path, q, &raw); err != nil {
		return nil, err
	}
	switch v := raw.(type) {
	case map[string]any:
		return v, nil
	case []any:
		return map[string]any{arrayKey: v}, nil
	case nil:
		return map[string]any{arrayKey: []any{}}, nil
	}
	return nil, fmt.Errorf("decode %s: unexpected JSON %T", path, raw)
}

func readList(ctx context.Context, get jsonGetter, path string, q url.Values) (*ListPage, error) {
	raw, err := readJSON(ctx, get, path, q, "items")
	if err != nil {
		return nil, err
	}
	return itemsFromResponse(raw), nil
}

func readRun(ctx context.Context, get jsonGetter, runID, suffix, kind string) (*ResourceItem, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("%w: run_id required", ErrInvalidParams)
	}
	raw, err := readJSON(ctx, get, "/api/v1/workflow-runs/"+url.PathEscape(runID)+suffix, nil, "events")
	if err != nil {
		return nil, err
	}
	return &ResourceItem{ID: runID, Kind: kind, Data: raw}, nil
}

func listJobs(ctx context.Context, get jsonGetter, req ListInput) (*ListPage, error) {
	return readList(ctx, get, "/api/v1/jobs", listQuery(req))
}

func listRuns(ctx context.Context, get jsonGetter, req ListInput) (*ListPage, error) {
	return readList(ctx, get, "/api/v1/workflow-runs", listQuery(req))
}

func listWorkflows(ctx context.Context, get jsonGetter, req ListInput) (*ListPage, error) {
	return readList(ctx, get, "/api/v1/workflows", listQuery(req))
}

func listTopics(ctx context.Context, get jsonGetter, req ListInput) (*ListPage, error) {
	return readList(ctx, get, "/api/v1/topics", listQuery(req))
}

func listAgents(ctx context.Context, get jsonGetter, req ListInput) (*ListPage, error) {
	return readList(ctx, get, "/api/v1/agents", listQuery(req))
}

func listPendingApprovals(ctx context.Context, get jsonGetter, req ListInput) (*ListPage, error) {
	q := listQuery(req)
	if q.Get("status") == "" {
		q.Set("status", "pending")
	}
	return readList(ctx, get, "/api/v1/approvals", q)
}

func verifyAudit(ctx context.Context, get jsonGetter, tenant string) (*ResourceItem, error) {
	q := url.Values{}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	raw, err := readJSON(ctx, get, "/api/v1/audit/verify", q, "result")
	if err != nil {
		return nil, err
	}
	return &ResourceItem{Kind: "audit_verify", Data: raw}, nil
}

// ListJobs — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListJobs(ctx context.Context, req ListInput) (*ListPage, error) {
	return listJobs(ctx, b.getJSON, req)
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
	return listRuns(ctx, b.getJSON, req)
}

// GetRun — HTTP bridge implementation.
func (b *HTTPServiceBridge) GetRun(ctx context.Context, runID string) (*ResourceItem, error) {
	return readRun(ctx, b.getJSON, runID, "", "run")
}

// GetRunTimeline — HTTP bridge implementation.
func (b *HTTPServiceBridge) GetRunTimeline(ctx context.Context, runID string) (*ResourceItem, error) {
	return readRun(ctx, b.getJSON, runID, "/timeline", "run_timeline")
}

// ListWorkflows — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListWorkflows(ctx context.Context, req ListInput) (*ListPage, error) {
	return listWorkflows(ctx, b.getJSON, req)
}

// ListPacks — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListPacks(ctx context.Context, req ListInput) (*ListPage, error) {
	return readList(ctx, b.getJSON, "/api/v1/packs", listQuery(req))
}

// ListTopics — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListTopics(ctx context.Context, req ListInput) (*ListPage, error) {
	return listTopics(ctx, b.getJSON, req)
}

// ListWorkers — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListWorkers(ctx context.Context, req ListInput) (*ListPage, error) {
	return readList(ctx, b.getJSON, "/api/v1/workers", listQuery(req))
}

// ListAgents — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListAgents(ctx context.Context, req ListInput) (*ListPage, error) {
	return listAgents(ctx, b.getJSON, req)
}

// ListPendingApprovals — HTTP bridge implementation.
func (b *HTTPServiceBridge) ListPendingApprovals(ctx context.Context, req ListInput) (*ListPage, error) {
	return listPendingApprovals(ctx, b.getJSON, req)
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
	return readList(ctx, b.getJSON, "/api/v1/audit/query", q)
}

// VerifyAudit — HTTP bridge implementation.
func (b *HTTPServiceBridge) VerifyAudit(ctx context.Context, tenant string) (*ResourceItem, error) {
	return verifyAudit(ctx, b.getJSON, tenant)
}

// GetStatus — HTTP bridge implementation.
func (b *HTTPServiceBridge) GetStatus(ctx context.Context) (*ResourceItem, error) {
	var raw map[string]any
	if err := b.getJSON(ctx, "/api/v1/status", nil, &raw); err != nil {
		return nil, err
	}
	return &ResourceItem{Kind: "status", Data: raw}, nil
}

// DirectServiceBridge read methods. GetJob and GetStatus call their own
// hooks, and nine reads call GetJSONFunc with the same route and query as
// HTTPServiceBridge. ListPacks, ListWorkers and QueryAudit always return
// ErrReadOnlyUnsupported's 501 BridgeError, as does any read whose hook is
// nil: only the stdio cordum-mcp binary, which uses HTTPServiceBridge,
// serves those three.

// reader returns the GetJSONFunc hook, or a getter that answers 501
// read_only_unsupported for method when the bridge has none.
func (b *DirectServiceBridge) reader(method string) jsonGetter {
	if b != nil && b.getJSON != nil {
		return b.getJSON
	}
	return func(context.Context, string, url.Values, any) error {
		return NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", method, nil)
	}
}

func (b *DirectServiceBridge) ListJobs(ctx context.Context, req ListInput) (*ListPage, error) {
	return listJobs(ctx, b.reader("ListJobs"), req)
}

// GetJob — direct bridge implementation through the GetJobFunc hook.
func (b *DirectServiceBridge) GetJob(ctx context.Context, jobID string) (*ResourceItem, error) {
	if b == nil || b.getJob == nil {
		return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "GetJob", nil)
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("%w: job_id required", ErrInvalidParams)
	}
	return b.getJob(ctx, jobID)
}
func (b *DirectServiceBridge) ListRuns(ctx context.Context, req ListInput) (*ListPage, error) {
	return listRuns(ctx, b.reader("ListRuns"), req)
}
func (b *DirectServiceBridge) GetRun(ctx context.Context, runID string) (*ResourceItem, error) {
	return readRun(ctx, b.reader("GetRun"), runID, "", "run")
}
func (b *DirectServiceBridge) GetRunTimeline(ctx context.Context, runID string) (*ResourceItem, error) {
	return readRun(ctx, b.reader("GetRunTimeline"), runID, "/timeline", "run_timeline")
}
func (b *DirectServiceBridge) ListWorkflows(ctx context.Context, req ListInput) (*ListPage, error) {
	return listWorkflows(ctx, b.reader("ListWorkflows"), req)
}
func (b *DirectServiceBridge) ListPacks(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListPacks", nil)
}
func (b *DirectServiceBridge) ListTopics(ctx context.Context, req ListInput) (*ListPage, error) {
	return listTopics(ctx, b.reader("ListTopics"), req)
}
func (b *DirectServiceBridge) ListWorkers(context.Context, ListInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "ListWorkers", nil)
}
func (b *DirectServiceBridge) ListAgents(ctx context.Context, req ListInput) (*ListPage, error) {
	return listAgents(ctx, b.reader("ListAgents"), req)
}
func (b *DirectServiceBridge) ListPendingApprovals(ctx context.Context, req ListInput) (*ListPage, error) {
	return listPendingApprovals(ctx, b.reader("ListPendingApprovals"), req)
}
func (b *DirectServiceBridge) QueryAudit(context.Context, AuditQueryInput) (*ListPage, error) {
	return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "QueryAudit", nil)
}
func (b *DirectServiceBridge) VerifyAudit(ctx context.Context, tenant string) (*ResourceItem, error) {
	return verifyAudit(ctx, b.reader("VerifyAudit"), tenant)
}

// GetStatus — direct bridge implementation through the GetStatusFunc hook.
func (b *DirectServiceBridge) GetStatus(ctx context.Context) (*ResourceItem, error) {
	if b == nil || b.getStatus == nil {
		return nil, NewBridgeError(http.StatusNotImplemented, "read_only_unsupported", "GetStatus", nil)
	}
	return b.getStatus(ctx)
}
