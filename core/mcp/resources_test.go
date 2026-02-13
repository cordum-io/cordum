package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

type mockDataBridge struct {
	getJob           func(ctx context.Context, id string) (*JobDetail, error)
	listJobs         func(ctx context.Context, opts JobListOpts) (*JobList, error)
	listWorkflowRuns func(ctx context.Context, wfID string, limit int) (*RunList, error)
	getWorkflowRun   func(ctx context.Context, wfID, runID string) (*RunDetail, error)
	listAuditEntries func(ctx context.Context, limit int) ([]AuditEntry, error)
	getSystemHealth  func(ctx context.Context) (*HealthStatus, error)
	listPolicies     func(ctx context.Context) (*PolicySummary, error)
}

func (m *mockDataBridge) GetJob(ctx context.Context, id string) (*JobDetail, error) {
	if m.getJob == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.getJob(ctx, id)
}

func (m *mockDataBridge) ListJobs(ctx context.Context, opts JobListOpts) (*JobList, error) {
	if m.listJobs == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.listJobs(ctx, opts)
}

func (m *mockDataBridge) ListWorkflowRuns(ctx context.Context, wfID string, limit int) (*RunList, error) {
	if m.listWorkflowRuns == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.listWorkflowRuns(ctx, wfID, limit)
}

func (m *mockDataBridge) GetWorkflowRun(ctx context.Context, wfID, runID string) (*RunDetail, error) {
	if m.getWorkflowRun == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.getWorkflowRun(ctx, wfID, runID)
}

func (m *mockDataBridge) ListAuditEntries(ctx context.Context, limit int) ([]AuditEntry, error) {
	if m.listAuditEntries == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.listAuditEntries(ctx, limit)
}

func (m *mockDataBridge) GetSystemHealth(ctx context.Context) (*HealthStatus, error) {
	if m.getSystemHealth == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.getSystemHealth(ctx)
}

func (m *mockDataBridge) ListPolicies(ctx context.Context) (*PolicySummary, error) {
	if m.listPolicies == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.listPolicies(ctx)
}

func TestGetJobResource(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		reg := newResourceRegistryForTest(t, &mockDataBridge{
			getJob: func(_ context.Context, id string) (*JobDetail, error) {
				if id != "job-1" {
					t.Fatalf("unexpected job id %q", id)
				}
				job := JobDetail{"id": id, "state": "running"}
				return &job, nil
			},
		})
		content, err := reg.Read(context.Background(), "cordum://jobs/job-1")
		if err != nil {
			t.Fatalf("read resource failed: %v", err)
		}
		payload := mustJSONMapFromResource(t, content)
		if payload["id"] != "job-1" {
			t.Fatalf("expected id=job-1, got %#v", payload["id"])
		}
	})

	t.Run("not found", func(t *testing.T) {
		reg := newResourceRegistryForTest(t, &mockDataBridge{
			getJob: func(_ context.Context, _ string) (*JobDetail, error) {
				return nil, NewBridgeError(http.StatusNotFound, "not_found", "job not found", nil)
			},
		})
		_, err := reg.Read(context.Background(), "cordum://jobs/missing")
		if !errors.Is(err, ErrResourceNotFound) {
			t.Fatalf("expected ErrResourceNotFound, got %v", err)
		}
	})
}

func TestListJobsResource(t *testing.T) {
	t.Parallel()

	t.Run("default pagination", func(t *testing.T) {
		reg := newResourceRegistryForTest(t, &mockDataBridge{
			listJobs: func(_ context.Context, opts JobListOpts) (*JobList, error) {
				if opts.Limit != 20 {
					t.Fatalf("expected default limit=20, got %d", opts.Limit)
				}
				return &JobList{Items: []map[string]any{{"id": "job-1"}}}, nil
			},
		})
		content, err := reg.Read(context.Background(), "cordum://jobs")
		if err != nil {
			t.Fatalf("read jobs list failed: %v", err)
		}
		payload := mustJSONMapFromResource(t, content)
		items, ok := payload["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("unexpected items payload: %#v", payload["items"])
		}
	})

	t.Run("status and cursor filters", func(t *testing.T) {
		reg := newResourceRegistryForTest(t, &mockDataBridge{
			listJobs: func(_ context.Context, opts JobListOpts) (*JobList, error) {
				if opts.Status != "running" {
					t.Fatalf("expected status=running, got %q", opts.Status)
				}
				if opts.Limit != 5 {
					t.Fatalf("expected limit=5, got %d", opts.Limit)
				}
				if opts.Cursor != 42 {
					t.Fatalf("expected cursor=42, got %d", opts.Cursor)
				}
				next := int64(41)
				return &JobList{
					Items:      []map[string]any{{"id": "job-1"}, {"id": "job-2"}},
					NextCursor: &next,
				}, nil
			},
		})
		content, err := reg.Read(context.Background(), "cordum://jobs?status=running&limit=5&cursor=42")
		if err != nil {
			t.Fatalf("read jobs list failed: %v", err)
		}
		payload := mustJSONMapFromResource(t, content)
		if payload["next_cursor"] != float64(41) {
			t.Fatalf("expected next_cursor=41, got %#v", payload["next_cursor"])
		}
	})
}

func TestListWorkflowRunsResource(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		reg := newResourceRegistryForTest(t, &mockDataBridge{
			listWorkflowRuns: func(_ context.Context, wfID string, limit int) (*RunList, error) {
				if wfID != "wf-1" {
					t.Fatalf("expected wfID=wf-1, got %q", wfID)
				}
				if limit != 10 {
					t.Fatalf("expected default limit=10, got %d", limit)
				}
				return &RunList{
					WorkflowID: wfID,
					Items:      []map[string]any{{"id": "run-1", "status": "pending"}},
				}, nil
			},
		})
		content, err := reg.Read(context.Background(), "cordum://workflows/wf-1/runs")
		if err != nil {
			t.Fatalf("read workflow runs failed: %v", err)
		}
		payload := mustJSONMapFromResource(t, content)
		if payload["workflow_id"] != "wf-1" {
			t.Fatalf("expected workflow_id=wf-1, got %#v", payload["workflow_id"])
		}
	})

	t.Run("not found", func(t *testing.T) {
		reg := newResourceRegistryForTest(t, &mockDataBridge{
			listWorkflowRuns: func(_ context.Context, _ string, _ int) (*RunList, error) {
				return nil, NewBridgeError(http.StatusNotFound, "not_found", "workflow not found", nil)
			},
		})
		_, err := reg.Read(context.Background(), "cordum://workflows/missing/runs")
		if !errors.Is(err, ErrResourceNotFound) {
			t.Fatalf("expected ErrResourceNotFound, got %v", err)
		}
	})
}

func TestGetWorkflowRunResource(t *testing.T) {
	t.Parallel()

	reg := newResourceRegistryForTest(t, &mockDataBridge{
		getWorkflowRun: func(_ context.Context, wfID, runID string) (*RunDetail, error) {
			if wfID != "wf-1" || runID != "run-1" {
				t.Fatalf("unexpected ids wf=%q run=%q", wfID, runID)
			}
			run := RunDetail{"id": runID, "workflow_id": wfID, "status": "running"}
			return &run, nil
		},
	})
	content, err := reg.Read(context.Background(), "cordum://workflows/wf-1/runs/run-1")
	if err != nil {
		t.Fatalf("read workflow run failed: %v", err)
	}
	payload := mustJSONMapFromResource(t, content)
	if payload["id"] != "run-1" {
		t.Fatalf("expected id=run-1, got %#v", payload["id"])
	}
}

func TestAuditHealthPoliciesResources(t *testing.T) {
	t.Parallel()

	reg := newResourceRegistryForTest(t, &mockDataBridge{
		listAuditEntries: func(_ context.Context, limit int) ([]AuditEntry, error) {
			if limit != 12 {
				t.Fatalf("expected limit=12, got %d", limit)
			}
			return []AuditEntry{{"action": "publish", "created_at": "2026-02-13T00:00:00Z"}}, nil
		},
		getSystemHealth: func(_ context.Context) (*HealthStatus, error) {
			status := HealthStatus{"workers": map[string]any{"count": 3}, "uptime_seconds": 100}
			return &status, nil
		},
		listPolicies: func(_ context.Context) (*PolicySummary, error) {
			summary := PolicySummary{
				"active_bundles": []map[string]any{{"id": "core/default", "rule_count": 4}},
				"safety_stance":  "balanced",
			}
			return &summary, nil
		},
	})

	auditContent, err := reg.Read(context.Background(), "cordum://audit?limit=12")
	if err != nil {
		t.Fatalf("read audit failed: %v", err)
	}
	auditPayload := mustJSONMapFromResource(t, auditContent)
	if _, ok := auditPayload["items"].([]any); !ok {
		t.Fatalf("expected audit items array, got %#v", auditPayload["items"])
	}

	healthContent, err := reg.Read(context.Background(), "cordum://health")
	if err != nil {
		t.Fatalf("read health failed: %v", err)
	}
	healthPayload := mustJSONMapFromResource(t, healthContent)
	if _, ok := healthPayload["workers"].(map[string]any); !ok {
		t.Fatalf("expected workers object, got %#v", healthPayload["workers"])
	}

	policiesContent, err := reg.Read(context.Background(), "cordum://policies")
	if err != nil {
		t.Fatalf("read policies failed: %v", err)
	}
	policiesPayload := mustJSONMapFromResource(t, policiesContent)
	if policiesPayload["safety_stance"] != "balanced" {
		t.Fatalf("expected safety_stance=balanced, got %#v", policiesPayload["safety_stance"])
	}
}

func TestResourceURIMatchingWithQuery(t *testing.T) {
	t.Parallel()

	reg := newResourceRegistryForTest(t, &mockDataBridge{
		listJobs: func(_ context.Context, opts JobListOpts) (*JobList, error) {
			if opts.Status != "succeeded" {
				t.Fatalf("expected status=succeeded, got %q", opts.Status)
			}
			return &JobList{Items: []map[string]any{{"id": "job-1"}}}, nil
		},
	})

	// Ensures registry can resolve exact resources even when query params exist.
	content, err := reg.Read(context.Background(), "cordum://jobs?status=succeeded")
	if err != nil {
		t.Fatalf("read jobs with query failed: %v", err)
	}
	payload := mustJSONMapFromResource(t, content)
	if _, ok := payload["items"]; !ok {
		t.Fatalf("expected items field in payload")
	}
}

func newResourceRegistryForTest(t *testing.T, bridge DataBridge) *ResourceRegistry {
	t.Helper()
	reg := NewResourceRegistry()
	if err := RegisterAllResources(reg, bridge); err != nil {
		t.Fatalf("register resources: %v", err)
	}
	return reg
}

func mustJSONMapFromResource(t *testing.T, content *ResourceContents) map[string]any {
	t.Helper()
	if content == nil {
		t.Fatal("resource content is nil")
	}
	if content.MIMEType != "application/json" {
		t.Fatalf("expected application/json mime type, got %q", content.MIMEType)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(content.Text), &out); err != nil {
		t.Fatalf("decode resource json: %v", err)
	}
	return out
}
