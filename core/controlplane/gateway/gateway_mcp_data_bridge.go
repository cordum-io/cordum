package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/mcp"
)

func (s *server) newMCPDataBridge() mcp.DataBridge {
	if s == nil {
		return mcp.NewDirectDataBridge(mcp.DirectDataBridgeConfig{})
	}
	return mcp.NewDirectDataBridge(mcp.DirectDataBridgeConfig{
		GetJobFunc: func(ctx context.Context, id string) (*mcp.JobDetail, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodGet,
				"/api/v1/jobs/"+strings.TrimSpace(id),
				nil,
				map[string]string{"id": strings.TrimSpace(id)},
				nil,
				s.handleGetJob,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			out := mcp.JobDetail(payload)
			return &out, nil
		},
		ListJobsFunc: func(ctx context.Context, opts mcp.JobListOpts) (*mcp.JobList, error) {
			values := []string{}
			if opts.Limit > 0 {
				values = append(values, "limit="+strconv.Itoa(opts.Limit))
			}
			if status := strings.TrimSpace(opts.Status); status != "" {
				values = append(values, "state="+status)
			}
			if opts.Cursor > 0 {
				values = append(values, "cursor="+strconv.FormatInt(opts.Cursor, 10))
			}
			target := "/api/v1/jobs"
			if len(values) > 0 {
				target += "?" + strings.Join(values, "&")
			}

			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, target, nil, nil, nil, s.handleListJobs)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := mcpGatewayToMapSlice(payload["items"])
			out := &mcp.JobList{Items: items}
			if next, ok := mcpGatewayToInt64(payload["next_cursor"]); ok {
				out.NextCursor = &next
			}
			return out, nil
		},
		ListWorkflowRunsFunc: func(ctx context.Context, wfID string, limit int) (*mcp.RunList, error) {
			target := "/api/v1/workflows/" + strings.TrimSpace(wfID) + "/runs"
			if limit > 0 {
				target += "?limit=" + strconv.Itoa(limit)
			}
			status, payload, raw, err := s.invokeMCPAnyHandler(
				ctx,
				http.MethodGet,
				target,
				nil,
				map[string]string{"id": strings.TrimSpace(wfID)},
				nil,
				s.handleListRuns,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := []map[string]any{}
			switch typed := payload.(type) {
			case []any:
				items = mcpGatewayToMapSlice(typed)
			case map[string]any:
				items = mcpGatewayToMapSlice(typed["items"])
			}
			return &mcp.RunList{
				WorkflowID: strings.TrimSpace(wfID),
				Items:      items,
			}, nil
		},
		GetWorkflowRunFunc: func(ctx context.Context, wfID, runID string) (*mcp.RunDetail, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodGet,
				"/api/v1/workflow-runs/"+strings.TrimSpace(runID),
				nil,
				map[string]string{"id": strings.TrimSpace(runID)},
				nil,
				s.handleGetRun,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			if expected := strings.TrimSpace(wfID); expected != "" {
				if actual := strings.TrimSpace(mcpBridgeString(payload["workflow_id"])); actual != "" && actual != expected {
					return nil, mcp.NewBridgeError(http.StatusNotFound, "not_found", "workflow run not found", nil)
				}
			}
			out := mcp.RunDetail(payload)
			return &out, nil
		},
		ListAuditEntriesFunc: func(ctx context.Context, limit int) ([]mcp.AuditEntry, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/policy/audit", nil, nil, nil, s.handleListPolicyAudit)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := mcpGatewayToMapSlice(payload["items"])
			if limit <= 0 {
				limit = 50
			}
			if len(items) > limit {
				items = items[:limit]
			}
			out := make([]mcp.AuditEntry, 0, len(items))
			for _, item := range items {
				out = append(out, mcp.AuditEntry(item))
			}
			return out, nil
		},
		GetSystemHealthFunc: func(ctx context.Context) (*mcp.HealthStatus, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/status", nil, nil, nil, s.handleStatus)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			out := mcp.HealthStatus(payload)
			return &out, nil
		},
		ListPoliciesSummaryFn: func(ctx context.Context) (*mcp.PolicySummary, error) {
			status, bundlesPayload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/policy/bundles", nil, nil, nil, s.handlePolicyBundles)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := mcpGatewayToMapSlice(bundlesPayload["items"])

			currentSnapshot := ""
			if status, snapshotsPayload, _, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/policy/snapshots", nil, nil, nil, s.handlePolicySnapshots); err == nil {
				if status >= 200 && status < 300 {
					if snapshots, ok := snapshotsPayload["snapshots"].([]any); ok && len(snapshots) > 0 {
						currentSnapshot = strings.TrimSpace(mcpBridgeString(snapshots[0]))
					}
				}
			}

			summary := mcp.PolicySummary{
				"active_bundles":      items,
				"current_snapshot_id": currentSnapshot,
				"safety_stance":       mcpGatewayInferSafetyStance(items),
			}
			return &summary, nil
		},
	})
}

func mcpGatewayToMapSlice(raw any) []map[string]any {
	if raw == nil {
		return []map[string]any{}
	}
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

func mcpGatewayToInt64(raw any) (int64, bool) {
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

func mcpGatewayInferSafetyStance(items []map[string]any) string {
	if len(items) == 0 {
		return "permissive"
	}
	enabled := 0
	var totalRules int64
	for _, item := range items {
		if v, ok := item["enabled"].(bool); ok && v {
			enabled++
		}
		if rc, ok := mcpGatewayToInt64(item["rule_count"]); ok {
			totalRules += rc
		}
	}
	if enabled == 0 || totalRules == 0 {
		return "permissive"
	}
	if totalRules >= 20 {
		return "strict"
	}
	return "balanced"
}
