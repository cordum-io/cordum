package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPDataBridge(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-a" {
			t.Fatalf("expected tenant header tenant-a, got %q", r.Header.Get("X-Tenant-ID"))
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs/job-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "job-1", "state": "running"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs":
			if r.URL.Query().Get("state") != "running" {
				t.Fatalf("expected state query running, got %q", r.URL.Query().Get("state"))
			}
			if r.URL.Query().Get("limit") != "5" {
				t.Fatalf("expected limit query 5, got %q", r.URL.Query().Get("limit"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":       []map[string]any{{"id": "job-1"}, {"id": "job-2"}},
				"next_cursor": 10,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workflows/wf-1/runs":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "run-1", "workflow_id": "wf-1"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workflow-runs/run-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "run-1", "workflow_id": "wf-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/policy/audit":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"action": "publish"}, {"action": "rollback"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"workers": map[string]any{"count": 2}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/policy/bundles":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "core/default", "enabled": true, "rule_count": 5}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/policy/snapshots":
			_ = json.NewEncoder(w).Encode(map[string]any{"snapshots": []string{"snap-1"}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	bridge := NewHTTPDataBridge(HTTPDataBridgeConfig{
		BaseURL:  srv.URL,
		TenantID: "tenant-a",
	})

	job, err := bridge.GetJob(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if (*job)["id"] != "job-1" {
		t.Fatalf("unexpected GetJob payload: %#v", *job)
	}

	list, err := bridge.ListJobs(context.Background(), JobListOpts{Status: "running", Limit: 5})
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("expected 2 job items, got %d", len(list.Items))
	}
	if list.NextCursor == nil || *list.NextCursor != 10 {
		t.Fatalf("unexpected next cursor: %#v", list.NextCursor)
	}

	runs, err := bridge.ListWorkflowRuns(context.Background(), "wf-1", 10)
	if err != nil {
		t.Fatalf("ListWorkflowRuns failed: %v", err)
	}
	if len(runs.Items) != 1 || runs.Items[0]["id"] != "run-1" {
		t.Fatalf("unexpected workflow runs payload: %#v", runs.Items)
	}

	run, err := bridge.GetWorkflowRun(context.Background(), "wf-1", "run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun failed: %v", err)
	}
	if (*run)["id"] != "run-1" {
		t.Fatalf("unexpected run payload: %#v", *run)
	}

	audit, err := bridge.ListAuditEntries(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListAuditEntries failed: %v", err)
	}
	if len(audit) != 1 {
		t.Fatalf("expected audit entries limited to 1, got %d", len(audit))
	}

	health, err := bridge.GetSystemHealth(context.Background())
	if err != nil {
		t.Fatalf("GetSystemHealth failed: %v", err)
	}
	if _, ok := (*health)["workers"].(map[string]any); !ok {
		t.Fatalf("unexpected health payload: %#v", *health)
	}

	policies, err := bridge.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListPolicies failed: %v", err)
	}
	if (*policies)["current_snapshot_id"] != "snap-1" {
		t.Fatalf("expected current_snapshot_id=snap-1, got %#v", (*policies)["current_snapshot_id"])
	}
	if (*policies)["safety_stance"] != "balanced" {
		t.Fatalf("expected safety_stance=balanced, got %#v", (*policies)["safety_stance"])
	}
}

func TestHTTPDataBridgeErrorMapping(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs/missing" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "not found"})
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "upstream"})
	}))
	defer srv.Close()

	bridge := NewHTTPDataBridge(HTTPDataBridgeConfig{
		BaseURL:  srv.URL,
		TenantID: "default",
	})

	_, err := bridge.GetJob(context.Background(), "missing")
	assertDataBridgeStatus(t, err, http.StatusNotFound)

	_, err = bridge.GetSystemHealth(context.Background())
	assertDataBridgeStatus(t, err, http.StatusBadGateway)
}

func TestDirectDataBridge(t *testing.T) {
	t.Parallel()

	bridge := NewDirectDataBridge(DirectDataBridgeConfig{
		GetJobFunc: func(_ context.Context, id string) (*JobDetail, error) {
			j := JobDetail{"id": id}
			return &j, nil
		},
		ListJobsFunc: func(_ context.Context, _ JobListOpts) (*JobList, error) {
			return &JobList{Items: []map[string]any{{"id": "job-1"}}}, nil
		},
		ListWorkflowRunsFunc: func(_ context.Context, wfID string, _ int) (*RunList, error) {
			return &RunList{WorkflowID: wfID, Items: []map[string]any{{"id": "run-1"}}}, nil
		},
		GetWorkflowRunFunc: func(_ context.Context, _, runID string) (*RunDetail, error) {
			r := RunDetail{"id": runID}
			return &r, nil
		},
		ListAuditEntriesFunc: func(_ context.Context, _ int) ([]AuditEntry, error) {
			return []AuditEntry{{"action": "publish"}}, nil
		},
		GetSystemHealthFunc: func(_ context.Context) (*HealthStatus, error) {
			h := HealthStatus{"ok": true}
			return &h, nil
		},
		ListPoliciesSummaryFn: func(_ context.Context) (*PolicySummary, error) {
			p := PolicySummary{"safety_stance": "balanced"}
			return &p, nil
		},
	})

	if _, err := bridge.GetJob(context.Background(), "job-1"); err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if _, err := bridge.ListJobs(context.Background(), JobListOpts{}); err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if _, err := bridge.ListWorkflowRuns(context.Background(), "wf-1", 10); err != nil {
		t.Fatalf("ListWorkflowRuns failed: %v", err)
	}
	if _, err := bridge.GetWorkflowRun(context.Background(), "wf-1", "run-1"); err != nil {
		t.Fatalf("GetWorkflowRun failed: %v", err)
	}
	if _, err := bridge.ListAuditEntries(context.Background(), 10); err != nil {
		t.Fatalf("ListAuditEntries failed: %v", err)
	}
	if _, err := bridge.GetSystemHealth(context.Background()); err != nil {
		t.Fatalf("GetSystemHealth failed: %v", err)
	}
	if _, err := bridge.ListPolicies(context.Background()); err != nil {
		t.Fatalf("ListPolicies failed: %v", err)
	}
}

func TestDirectDataBridgeUnavailable(t *testing.T) {
	t.Parallel()

	bridge := NewDirectDataBridge(DirectDataBridgeConfig{})
	if _, err := bridge.GetJob(context.Background(), "job-1"); !errors.Is(err, ErrBridgeUnavailable) {
		t.Fatalf("expected ErrBridgeUnavailable, got %v", err)
	}
}

func assertDataBridgeStatus(t *testing.T, err error, status int) {
	t.Helper()
	var be *BridgeError
	if !errors.As(err, &be) {
		t.Fatalf("expected BridgeError, got %v", err)
	}
	if be.StatusCode != status {
		t.Fatalf("expected status=%d, got %d", status, be.StatusCode)
	}
}
