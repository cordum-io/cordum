package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestClassifyLockType(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"cordum:reconciler:default", "reconciler"},
		{"cordum:replayer:pending", "replayer"},
		{"cordum:scheduler:job:job-123", "job"},
		{"cordum:scheduler:snapshot:writer", "snapshot"},
		{"cordum:dlq:cleanup", "dlq_cleanup"},
		{"cordum:wf:run:lock:run-abc", "workflow_run"},
		{"cordum:wf:delay:poller", "delay_poller"},
		{"cordum:workflow-engine:reconciler:default", "workflow_reconciler"},
		{"cordum:rl:key:12345", "rate_limit"},
		{"cordum:auth:jwks:abc123", "jwks_cache"},
		{"cordum:cb:safety:failures", "circuit_breaker"},
		{"cordum:cache:marketplace", "marketplace_cache"},
		{"cordum:unknown:something", "unknown"},
		{"totally:different:key", "unknown"},
	}
	for _, tt := range tests {
		got := classifyLockType(tt.key)
		if got != tt.want {
			t.Errorf("classifyLockType(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestHandleAdminLocksEmpty(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/locks", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleAdminLocks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	locks, ok := resp["locks"].([]any)
	if !ok {
		t.Fatalf("expected locks array, got %T", resp["locks"])
	}
	if len(locks) != 0 {
		t.Fatalf("expected 0 locks, got %d", len(locks))
	}
}

func TestHandleAdminLocksWithSeededKeys(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	rdb := s.jobStore.Client()

	// Seed known lock keys.
	rdb.Set(ctx, "cordum:reconciler:default", "scheduler-abc", 30*time.Second)
	rdb.Set(ctx, "cordum:scheduler:job:job-1", "scheduler-def", 25*time.Second)
	rdb.Set(ctx, "cordum:wf:run:lock:run-xyz", "wf-engine-ghi", 20*time.Second)
	rdb.Set(ctx, "cordum:dlq:cleanup", "gateway-jkl", 15*time.Second)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/locks", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleAdminLocks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	locks := resp["locks"].([]any)
	if len(locks) != 4 {
		t.Fatalf("expected 4 locks, got %d", len(locks))
	}

	// Verify each entry has required fields.
	for _, l := range locks {
		entry := l.(map[string]any)
		if _, ok := entry["key"].(string); !ok {
			t.Fatal("missing key field")
		}
		if _, ok := entry["holder"].(string); !ok {
			t.Fatal("missing holder field")
		}
		if _, ok := entry["type"].(string); !ok {
			t.Fatal("missing type field")
		}
		if _, ok := entry["ttl_remaining_ms"].(float64); !ok {
			t.Fatal("missing ttl_remaining_ms field")
		}
	}

	// Verify classification.
	typeMap := map[string]string{}
	for _, l := range locks {
		entry := l.(map[string]any)
		typeMap[entry["key"].(string)] = entry["type"].(string)
	}
	if typeMap["cordum:reconciler:default"] != "reconciler" {
		t.Fatalf("expected reconciler type, got %s", typeMap["cordum:reconciler:default"])
	}
	if typeMap["cordum:scheduler:job:job-1"] != "job" {
		t.Fatalf("expected job type, got %s", typeMap["cordum:scheduler:job:job-1"])
	}
}

func TestHandleAdminLocksRequiresAdmin(t *testing.T) {
	// Server with no auth provider — requireRole should fail.
	s := &server{
		workers:    map[string]*pb.Heartbeat{},
		workerSeen: map[string]time.Time{},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/locks", nil)
	rec := httptest.NewRecorder()
	s.handleAdminLocks(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("expected non-200 without auth, got 200")
	}
}

func TestHandleAdminLocksNilJobStore(t *testing.T) {
	s := &server{
		workers:    map[string]*pb.Heartbeat{},
		workerSeen: map[string]time.Time{},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/locks", nil)
	rec := httptest.NewRecorder()
	s.handleAdminLocks(rec, req)
	// Should return 403 (no auth) or 503 (no jobStore), not panic.
	if rec.Code == http.StatusOK {
		t.Fatal("expected non-200 with nil jobStore")
	}
}

func TestScanLocksCap(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	rdb := s.jobStore.Client()

	// Seed more keys than the limit.
	for i := 0; i < 10; i++ {
		rdb.Set(ctx, fmt.Sprintf("cordum:scheduler:job:job-%d", i), fmt.Sprintf("holder-%d", i), 30*time.Second)
	}

	entries, err := scanLocks(ctx, rdb, "cordum:scheduler:job:*", 5)
	if err != nil {
		t.Fatalf("scanLocks: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries (capped), got %d", len(entries))
	}
}
