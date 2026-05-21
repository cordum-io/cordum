package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/registry"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func seedSnapshot(t *testing.T, s *server, snap registry.Snapshot) {
	t.Helper()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := s.memStore.PutResult(context.Background(), registry.SnapshotKey, data); err != nil {
		t.Fatalf("put snapshot: %v", err)
	}
}

func testSnapshot() registry.Snapshot {
	return registry.Snapshot{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Workers: []registry.WorkerSummary{
			{
				WorkerID:        "w1",
				Pool:            "pool-a",
				ActiveJobs:      2,
				MaxParallelJobs: 4,
				Capabilities:    []string{"code"},
				CpuLoad:         45.0,
				MemoryLoad:      60.0,
				Region:          "us-east-1",
				Type:            "gpu",
				Labels:          map[string]string{"env": "prod"},
			},
			{
				WorkerID:        "w2",
				Pool:            "pool-a",
				ActiveJobs:      0,
				MaxParallelJobs: 4,
				Capabilities:    []string{"code", "review"},
				Region:          "eu-west-1",
				Type:            "cpu",
			},
			{
				WorkerID:        "w3",
				Pool:            "pool-b",
				ActiveJobs:      1,
				MaxParallelJobs: 2,
			},
		},
		Pools: map[string]registry.PoolSnapshot{
			"pool-a": {Workers: 2, ActiveJobs: 2, Capacity: 8},
			"pool-b": {Workers: 1, ActiveJobs: 1, Capacity: 2},
		},
		Topics: map[string]registry.TopicSnapshot{
			"job.code.run":   {Pool: "pool-a", Workers: 2, Capacity: 8, Available: true},
			"job.review.run": {Pool: "pool-a", Workers: 2, Capacity: 8, Available: true},
			"job.deploy.run": {Pool: "pool-b", Workers: 1, Capacity: 2, Available: true},
		},
	}
}

func TestHandleGetWorker_Found(t *testing.T) {
	s, _, _ := newTestGateway(t)
	snap := testSnapshot()
	seedSnapshot(t, s, snap)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["worker_id"] != "w1" {
		t.Fatalf("expected worker_id=w1, got %v", resp["worker_id"])
	}
	if resp["pool"] != "pool-a" {
		t.Fatalf("expected pool=pool-a, got %v", resp["pool"])
	}
	if resp["region"] != "us-east-1" {
		t.Fatalf("expected region=us-east-1, got %v", resp["region"])
	}
	if resp["type"] != "gpu" {
		t.Fatalf("expected type=gpu, got %v", resp["type"])
	}
	if resp["last_heartbeat"] == nil || resp["last_heartbeat"] == "" {
		t.Fatal("expected last_heartbeat to be set")
	}
	labels, ok := resp["labels"].(map[string]any)
	if !ok || labels["env"] != "prod" {
		t.Fatalf("expected labels.env=prod, got %v", resp["labels"])
	}
}

func TestHandleGetWorker_NotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)
	snap := testSnapshot()
	seedSnapshot(t, s, snap)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusNotFound, "WORKER_NOT_FOUND")
}

func TestHandleGetWorker_EmptyID(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/", nil)
	req.SetPathValue("id", "")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusBadRequest, "WORKER_SESSION_INVALID")
}

func TestHandleGetWorker_FallbackToInMemory(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// No snapshot in Redis — should fall back to in-memory.
	s.workerMu.Lock()
	s.workers["mem-w1"] = &pb.Heartbeat{
		WorkerId:        "mem-w1",
		Pool:            "pool-mem",
		ActiveJobs:      1,
		MaxParallelJobs: 2,
		Region:          "local",
		Type:            "cpu",
	}
	s.workerMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/mem-w1", nil)
	req.SetPathValue("id", "mem-w1")
	rec := httptest.NewRecorder()
	s.handleGetWorker(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["worker_id"] != "mem-w1" {
		t.Fatalf("expected worker_id=mem-w1, got %v", resp["worker_id"])
	}
	if resp["region"] != "local" {
		t.Fatalf("expected region=local, got %v", resp["region"])
	}
}

func TestHandleGetWorkerJobs_EmptyResult(t *testing.T) {
	s, _, _ := newTestGateway(t)
	snap := testSnapshot()
	seedSnapshot(t, s, snap)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/w1/jobs", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.handleGetWorkerJobs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", resp["items"])
	}
	if len(items) != 0 {
		t.Fatalf("expected empty items, got %d", len(items))
	}
}

func TestHandleGetWorkerJobs_NotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers/nonexistent/jobs", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	s.handleGetWorkerJobs(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusNotFound, "WORKER_NOT_FOUND")
}

func TestHandleListPools(t *testing.T) {
	s, _, _ := newTestGateway(t)
	snap := testSnapshot()
	seedSnapshot(t, s, snap)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
	rec := httptest.NewRecorder()
	s.handleListPools(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", resp["items"])
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(items))
	}

	// Verify pools have utilization calculated.
	poolsByName := map[string]map[string]any{}
	for _, item := range items {
		pool := item.(map[string]any)
		poolsByName[pool["name"].(string)] = pool
	}

	poolA, ok := poolsByName["pool-a"]
	if !ok {
		t.Fatal("expected pool-a in response")
	}
	// pool-a: 2 active / 8 capacity = 0.25
	util := poolA["utilization"].(float64)
	if util < 0.24 || util > 0.26 {
		t.Fatalf("expected pool-a utilization ~0.25, got %f", util)
	}

	poolB, ok := poolsByName["pool-b"]
	if !ok {
		t.Fatal("expected pool-b in response")
	}
	// pool-b: 1 active / 2 capacity = 0.5
	utilB := poolB["utilization"].(float64)
	if utilB < 0.49 || utilB > 0.51 {
		t.Fatalf("expected pool-b utilization ~0.5, got %f", utilB)
	}
}

func TestHandleListPools_EmptySnapshot(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// No snapshot seeded — should return empty list.

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
	rec := httptest.NewRecorder()
	s.handleListPools(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", resp["items"])
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 pools, got %d", len(items))
	}
}

func TestHandleGetPool_Found(t *testing.T) {
	s, _, _ := newTestGateway(t)
	snap := testSnapshot()
	seedSnapshot(t, s, snap)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pools/pool-a", nil)
	req.SetPathValue("name", "pool-a")
	rec := httptest.NewRecorder()
	s.handleGetPool(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["name"] != "pool-a" {
		t.Fatalf("expected name=pool-a, got %v", resp["name"])
	}

	workers, ok := resp["workers"].([]any)
	if !ok {
		t.Fatalf("expected workers array, got %T", resp["workers"])
	}
	if len(workers) != 2 {
		t.Fatalf("expected 2 workers in pool-a, got %d", len(workers))
	}

	topics, ok := resp["topics"].([]any)
	if !ok {
		t.Fatalf("expected topics array, got %T", resp["topics"])
	}
	if len(topics) != 2 {
		t.Fatalf("expected 2 topics for pool-a, got %d", len(topics))
	}

	// Verify utilization.
	util := resp["utilization"].(float64)
	if util < 0.24 || util > 0.26 {
		t.Fatalf("expected utilization ~0.25, got %f", util)
	}
}

func TestHandleGetPool_NotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)
	snap := testSnapshot()
	seedSnapshot(t, s, snap)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pools/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	rec := httptest.NewRecorder()
	s.handleGetPool(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusNotFound, "POOL_NOT_FOUND")
}

func TestPoolUtilization(t *testing.T) {
	cases := []struct {
		name     string
		pool     registry.PoolSnapshot
		expected float64
	}{
		{"zero capacity", registry.PoolSnapshot{Capacity: 0, ActiveJobs: 5}, 0},
		{"half used", registry.PoolSnapshot{Capacity: 10, ActiveJobs: 5}, 0.5},
		{"fully used", registry.PoolSnapshot{Capacity: 4, ActiveJobs: 4}, 1.0},
		{"empty", registry.PoolSnapshot{Capacity: 4, ActiveJobs: 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := poolUtilization(tc.pool)
			if got < tc.expected-0.01 || got > tc.expected+0.01 {
				t.Fatalf("expected %f, got %f", tc.expected, got)
			}
		})
	}
}
