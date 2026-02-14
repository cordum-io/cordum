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
)

const (
	ResourceJobsDetailTemplate   = "cordum://jobs/{id}"
	ResourceJobsListURI          = "cordum://jobs"
	ResourceWorkflowRunsTemplate = "cordum://workflows/{id}/runs"
	ResourceWorkflowRunTemplate  = "cordum://workflows/{id}/runs/{runId}"
	ResourceAuditURI             = "cordum://audit"
	ResourceHealthURI            = "cordum://health"
	ResourcePoliciesURI          = "cordum://policies"
)

type JobDetail map[string]any
type RunDetail map[string]any
type AuditEntry map[string]any
type HealthStatus map[string]any
type PolicySummary map[string]any

type JobListOpts struct {
	Status string
	Limit  int
	Cursor int64
}

type JobList struct {
	Items      []map[string]any `json:"items"`
	NextCursor *int64           `json:"next_cursor,omitempty"`
}

type RunList struct {
	WorkflowID string           `json:"workflow_id"`
	Items      []map[string]any `json:"items"`
}

// DataBridge abstracts backend reads used by MCP resource handlers.
type DataBridge interface {
	GetJob(ctx context.Context, id string) (*JobDetail, error)
	ListJobs(ctx context.Context, opts JobListOpts) (*JobList, error)
	ListWorkflowRuns(ctx context.Context, wfID string, limit int) (*RunList, error)
	GetWorkflowRun(ctx context.Context, wfID, runID string) (*RunDetail, error)
	ListAuditEntries(ctx context.Context, limit int) ([]AuditEntry, error)
	GetSystemHealth(ctx context.Context) (*HealthStatus, error)
	ListPolicies(ctx context.Context) (*PolicySummary, error)
}

// RegisterAllResources registers all core Cordum MCP resources.
func RegisterAllResources(registry *ResourceRegistry, bridge DataBridge) error {
	if registry == nil {
		return fmt.Errorf("resource registry is nil")
	}
	if bridge == nil {
		return fmt.Errorf("data bridge is nil")
	}

	specs := []struct {
		resource Resource
		handler  ResourceHandler
		template bool
	}{
		{
			resource: ResourceTemplate{
				URITemplate: ResourceJobsDetailTemplate,
				Name:        "cordum.jobs.detail",
				Description: "Job detail with status/result/safety metadata.",
				MIMEType:    "application/json",
			}.toResource(),
			handler:  jobDetailResourceHandler(bridge),
			template: true,
		},
		{
			resource: Resource{
				URI:         ResourceJobsListURI,
				Name:        "cordum.jobs.list",
				Description: "Job list resource with status/limit/cursor filters.",
				MIMEType:    "application/json",
			},
			handler: jobsListResourceHandler(bridge),
		},
		{
			resource: ResourceTemplate{
				URITemplate: ResourceWorkflowRunsTemplate,
				Name:        "cordum.workflows.runs",
				Description: "Workflow run list for a workflow ID.",
				MIMEType:    "application/json",
			}.toResource(),
			handler:  workflowRunsResourceHandler(bridge),
			template: true,
		},
		{
			resource: ResourceTemplate{
				URITemplate: ResourceWorkflowRunTemplate,
				Name:        "cordum.workflows.run",
				Description: "Single workflow run detail (steps/input/output/timeline links).",
				MIMEType:    "application/json",
			}.toResource(),
			handler:  workflowRunDetailResourceHandler(bridge),
			template: true,
		},
		{
			resource: Resource{
				URI:         ResourceAuditURI,
				Name:        "cordum.audit.list",
				Description: "Recent policy audit entries.",
				MIMEType:    "application/json",
			},
			handler: auditResourceHandler(bridge),
		},
		{
			resource: Resource{
				URI:         ResourceHealthURI,
				Name:        "cordum.system.health",
				Description: "System health status (workers, Redis, NATS, uptime).",
				MIMEType:    "application/json",
			},
			handler: healthResourceHandler(bridge),
		},
		{
			resource: Resource{
				URI:         ResourcePoliciesURI,
				Name:        "cordum.policies.summary",
				Description: "Active policy bundle and snapshot summary.",
				MIMEType:    "application/json",
			},
			handler: policiesResourceHandler(bridge),
		},
	}

	for _, spec := range specs {
		if spec.template {
			tmpl := ResourceTemplate{
				URITemplate: spec.resource.URI,
				Name:        spec.resource.Name,
				Description: spec.resource.Description,
				MIMEType:    spec.resource.MIMEType,
			}
			if err := registry.RegisterTemplate(tmpl, spec.handler); err != nil {
				return err
			}
			continue
		}
		if err := registry.Register(spec.resource, spec.handler); err != nil {
			return err
		}
	}
	return nil
}

func jobDetailResourceHandler(bridge DataBridge) ResourceHandler {
	return func(ctx context.Context, uri string) (*ResourceContents, error) {
		parsed, segments, err := parseCordumResourceURI(uri)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(parsed.Host, "jobs") || len(segments) != 1 {
			return nil, fmt.Errorf("%w: invalid jobs detail uri", ErrInvalidParams)
		}
		job, err := bridge.GetJob(ctx, strings.TrimSpace(segments[0]))
		if err != nil {
			return nil, mapResourceBridgeError(err)
		}
		if job == nil {
			return nil, ErrResourceNotFound
		}
		return jsonResource(uri, map[string]any(*job))
	}
}

func jobsListResourceHandler(bridge DataBridge) ResourceHandler {
	return func(ctx context.Context, uri string) (*ResourceContents, error) {
		parsed, segments, err := parseCordumResourceURI(uri)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(parsed.Host, "jobs") || len(segments) != 0 {
			return nil, fmt.Errorf("%w: invalid jobs list uri", ErrInvalidParams)
		}
		query := parsed.Query()
		limit := parseLimit(query.Get("limit"), 20, 100)
		cursor, _ := strconv.ParseInt(strings.TrimSpace(query.Get("cursor")), 10, 64)
		opts := JobListOpts{
			Status: strings.TrimSpace(query.Get("status")),
			Limit:  limit,
			Cursor: cursor,
		}
		list, err := bridge.ListJobs(ctx, opts)
		if err != nil {
			return nil, mapResourceBridgeError(err)
		}
		if list == nil {
			list = &JobList{Items: []map[string]any{}}
		}
		payload := map[string]any{
			"items": list.Items,
		}
		if list.NextCursor != nil {
			payload["next_cursor"] = *list.NextCursor
		}
		return jsonResource(uri, payload)
	}
}

func workflowRunsResourceHandler(bridge DataBridge) ResourceHandler {
	return func(ctx context.Context, uri string) (*ResourceContents, error) {
		parsed, segments, err := parseCordumResourceURI(uri)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(parsed.Host, "workflows") || len(segments) != 2 || segments[1] != "runs" {
			return nil, fmt.Errorf("%w: invalid workflow runs uri", ErrInvalidParams)
		}
		workflowID := strings.TrimSpace(segments[0])
		limit := parseLimit(parsed.Query().Get("limit"), 10, 100)
		list, err := bridge.ListWorkflowRuns(ctx, workflowID, limit)
		if err != nil {
			return nil, mapResourceBridgeError(err)
		}
		if list == nil {
			list = &RunList{WorkflowID: workflowID, Items: []map[string]any{}}
		}
		payload := map[string]any{
			"workflow_id": list.WorkflowID,
			"items":       list.Items,
		}
		return jsonResource(uri, payload)
	}
}

func workflowRunDetailResourceHandler(bridge DataBridge) ResourceHandler {
	return func(ctx context.Context, uri string) (*ResourceContents, error) {
		parsed, segments, err := parseCordumResourceURI(uri)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(parsed.Host, "workflows") || len(segments) != 3 || segments[1] != "runs" {
			return nil, fmt.Errorf("%w: invalid workflow run uri", ErrInvalidParams)
		}
		workflowID := strings.TrimSpace(segments[0])
		runID := strings.TrimSpace(segments[2])
		run, err := bridge.GetWorkflowRun(ctx, workflowID, runID)
		if err != nil {
			return nil, mapResourceBridgeError(err)
		}
		if run == nil {
			return nil, ErrResourceNotFound
		}
		return jsonResource(uri, map[string]any(*run))
	}
}

func auditResourceHandler(bridge DataBridge) ResourceHandler {
	return func(ctx context.Context, uri string) (*ResourceContents, error) {
		parsed, segments, err := parseCordumResourceURI(uri)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(parsed.Host, "audit") || len(segments) != 0 {
			return nil, fmt.Errorf("%w: invalid audit uri", ErrInvalidParams)
		}
		limit := parseLimit(parsed.Query().Get("limit"), 50, 200)
		entries, err := bridge.ListAuditEntries(ctx, limit)
		if err != nil {
			return nil, mapResourceBridgeError(err)
		}
		payload := map[string]any{"items": entries}
		return jsonResource(uri, payload)
	}
}

func healthResourceHandler(bridge DataBridge) ResourceHandler {
	return func(ctx context.Context, uri string) (*ResourceContents, error) {
		parsed, segments, err := parseCordumResourceURI(uri)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(parsed.Host, "health") || len(segments) != 0 {
			return nil, fmt.Errorf("%w: invalid health uri", ErrInvalidParams)
		}
		status, err := bridge.GetSystemHealth(ctx)
		if err != nil {
			return nil, mapResourceBridgeError(err)
		}
		if status == nil {
			status = &HealthStatus{}
		}
		return jsonResource(uri, map[string]any(*status))
	}
}

func policiesResourceHandler(bridge DataBridge) ResourceHandler {
	return func(ctx context.Context, uri string) (*ResourceContents, error) {
		parsed, segments, err := parseCordumResourceURI(uri)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(parsed.Host, "policies") || len(segments) != 0 {
			return nil, fmt.Errorf("%w: invalid policies uri", ErrInvalidParams)
		}
		summary, err := bridge.ListPolicies(ctx)
		if err != nil {
			return nil, mapResourceBridgeError(err)
		}
		if summary == nil {
			summary = &PolicySummary{}
		}
		return jsonResource(uri, map[string]any(*summary))
	}
}

func parseCordumResourceURI(rawURI string) (*url.URL, []string, error) {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return nil, nil, fmt.Errorf("%w: uri is required", ErrInvalidParams)
	}
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: invalid uri", ErrInvalidParams)
	}
	if !strings.EqualFold(parsed.Scheme, "cordum") {
		return nil, nil, fmt.Errorf("%w: unsupported uri scheme", ErrInvalidParams)
	}
	path := strings.Trim(parsed.Path, "/")
	if path == "" {
		return parsed, []string{}, nil
	}
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i], err = url.PathUnescape(strings.TrimSpace(parts[i]))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: invalid uri path", ErrInvalidParams)
		}
	}
	return parsed, parts, nil
}

func mapResourceBridgeError(err error) error {
	if err == nil {
		return nil
	}
	var be *BridgeError
	if errors.As(err, &be) {
		switch be.StatusCode {
		case http.StatusNotFound:
			return ErrResourceNotFound
		case http.StatusBadRequest:
			return fmt.Errorf("%w: %s", ErrInvalidParams, be.Message)
		}
	}
	return err
}

func parseLimit(raw string, defaultValue, maxValue int) int {
	limit := defaultValue
	if v, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && v > 0 {
		limit = v
	}
	if limit > maxValue {
		return maxValue
	}
	return limit
}

func jsonResource(uri string, payload any) (*ResourceContents, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode resource response: %w", err)
	}
	return &ResourceContents{
		URI:      uri,
		MIMEType: "application/json",
		Text:     string(data),
	}, nil
}

func (t ResourceTemplate) toResource() Resource {
	return Resource{
		URI:         t.URITemplate,
		Name:        t.Name,
		Description: t.Description,
		MIMEType:    t.MIMEType,
	}
}
