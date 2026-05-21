package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newMockBridge stands up a test HTTP server that records the path+method
// of every request and returns the canned payload.
func newMockBridge(t *testing.T, status int, body any) (*HTTPServiceBridge, *mockServer) {
	t.Helper()
	ms := &mockServer{body: body, status: status}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ms.lastMethod = r.Method
		ms.lastPath = r.URL.Path
		ms.lastQuery = r.URL.RawQuery
		ms.lastTenant = r.Header.Get("X-Tenant-ID")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ms.status)
		_ = json.NewEncoder(w).Encode(ms.body)
	}))
	t.Cleanup(srv.Close)
	bridge := NewHTTPServiceBridge(HTTPServiceBridgeConfig{
		BaseURL:           srv.URL,
		TenantID:          "default",
		AllowPrivateHosts: true, // httptest binds to 127.0.0.1
	}.WithAuthToken("test-key"))
	return bridge, ms
}

type mockServer struct {
	status                                      int
	body                                        any
	lastMethod, lastPath, lastQuery, lastTenant string
}

func TestHTTPBridge_ListJobs_CallsExpectedEndpoint(t *testing.T) {
	bridge, ms := newMockBridge(t, http.StatusOK, map[string]any{
		"items":       []map[string]any{{"id": "job-1"}, {"id": "job-2"}},
		"next_cursor": "abc",
		"total":       42,
	})
	page, err := bridge.ListJobs(context.Background(), ListInput{PageSize: 50, Cursor: "xyz"})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if ms.lastPath != "/api/v1/jobs" {
		t.Errorf("path = %q want /api/v1/jobs", ms.lastPath)
	}
	if !strings.Contains(ms.lastQuery, "limit=50") {
		t.Errorf("query missing limit: %q", ms.lastQuery)
	}
	if !strings.Contains(ms.lastQuery, "cursor=xyz") {
		t.Errorf("query missing cursor: %q", ms.lastQuery)
	}
	if ms.lastTenant != "default" {
		t.Errorf("tenant header = %q", ms.lastTenant)
	}
	if page.NextCursor != "abc" {
		t.Errorf("NextCursor = %q", page.NextCursor)
	}
	if len(page.Items) != 2 {
		t.Errorf("items len = %d", len(page.Items))
	}
}

func TestHTTPBridge_GetJob_ValidatesID(t *testing.T) {
	bridge, _ := newMockBridge(t, http.StatusOK, map[string]any{"id": "j"})
	if _, err := bridge.GetJob(context.Background(), ""); err == nil {
		t.Fatal("want error on empty id")
	}
}

func TestHTTPBridge_GetRunTimeline_PathFormat(t *testing.T) {
	bridge, ms := newMockBridge(t, http.StatusOK, map[string]any{"events": []any{}})
	if _, err := bridge.GetRunTimeline(context.Background(), "run-abc"); err != nil {
		t.Fatalf("GetRunTimeline: %v", err)
	}
	if ms.lastPath != "/api/v1/runs/run-abc/timeline" {
		t.Errorf("path = %q", ms.lastPath)
	}
}

func TestHTTPBridge_MapsHTTPErrorToBridgeError(t *testing.T) {
	bridge, _ := newMockBridge(t, http.StatusForbidden, map[string]any{"error": "forbidden"})
	_, err := bridge.ListJobs(context.Background(), ListInput{})
	if err == nil {
		t.Fatal("want error")
	}
	var berr *BridgeError
	if !errorsAs(err, &berr) {
		t.Fatalf("want BridgeError, got %T", err)
	}
	if berr.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d", berr.StatusCode)
	}
}

func TestHTTPBridge_ListPendingApprovals_DefaultsStatus(t *testing.T) {
	bridge, ms := newMockBridge(t, http.StatusOK, map[string]any{"items": []any{}})
	if _, err := bridge.ListPendingApprovals(context.Background(), ListInput{}); err != nil {
		t.Fatalf("ListPendingApprovals: %v", err)
	}
	if !strings.Contains(ms.lastQuery, "status=pending") {
		t.Errorf("status=pending missing: %q", ms.lastQuery)
	}
}

func TestHTTPBridge_QueryAudit_PropagatesFilters(t *testing.T) {
	bridge, ms := newMockBridge(t, http.StatusOK, map[string]any{"items": []any{}})
	if _, err := bridge.QueryAudit(context.Background(), AuditQueryInput{
		ListInput: ListInput{PageSize: 25},
		EventType: "mcp.tool_approval",
		Since:     "2026-04-17T00:00:00Z",
	}); err != nil {
		t.Fatalf("QueryAudit: %v", err)
	}
	q := ms.lastQuery
	for _, want := range []string{"type=mcp.tool_approval", "since=", "limit=25"} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q: %q", want, q)
		}
	}
}

func TestDirectBridge_ReadMethodsReturnNotImplemented(t *testing.T) {
	b := NewDirectServiceBridge(DirectServiceBridgeConfig{})
	_, err := b.ListJobs(context.Background(), ListInput{})
	if err == nil {
		t.Fatal("want error from DirectServiceBridge")
	}
	var berr *BridgeError
	if !errorsAs(err, &berr) || berr.StatusCode != http.StatusNotImplemented {
		t.Errorf("want 501 BridgeError, got %v", err)
	}
}

// errorsAs is a tiny shim so tests don't have to import errors just for As.
func errorsAs(err error, target any) bool {
	if err == nil {
		return false
	}
	if berr, ok := target.(**BridgeError); ok {
		var target *BridgeError
		for e := err; e != nil; {
			if v, ok := e.(*BridgeError); ok {
				target = v
				break
			}
			unwrap, ok := e.(interface{ Unwrap() error })
			if !ok {
				break
			}
			e = unwrap.Unwrap()
		}
		if target != nil {
			*berr = target
			return true
		}
	}
	return false
}
