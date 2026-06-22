package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestHandleStatusAndWorkers(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.workerMu.Lock()
	s.workers["w1"] = &pb.Heartbeat{WorkerId: "w1"}
	s.workerMu.Unlock()

	workersReq := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
	workersReq.Header.Set("X-Tenant-ID", "default")
	workersRec := httptest.NewRecorder()
	s.handleGetWorkers(workersRec, workersReq)
	if workersRec.Code != http.StatusOK {
		t.Fatalf("unexpected workers status: %d", workersRec.Code)
	}
	var workersResp struct{ Items []*pb.Heartbeat }
	if err := json.NewDecoder(workersRec.Body).Decode(&workersResp); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(workersResp.Items) != 1 || workersResp.Items[0].WorkerId != "w1" {
		t.Fatalf("unexpected workers list")
	}

	setTestEntitlements(t, s, licensing.PlanEnterprise, nil)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	statusReq.Header.Set("X-Tenant-ID", "default")
	statusRec := httptest.NewRecorder()
	s.handleStatus(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", statusRec.Code)
	}
	var status map[string]any
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	workersInfo, ok := status["workers"].(map[string]any)
	if !ok || workersInfo["count"].(float64) != 1 {
		t.Fatalf("unexpected workers count in status")
	}
	pipelineInfo, ok := status["pipeline"].(map[string]any)
	if !ok {
		t.Fatalf("expected pipeline info in status")
	}
	if pipelineInfo["pending"].(float64) != 0 ||
		pipelineInfo["dispatched"].(float64) != 0 ||
		pipelineInfo["running"].(float64) != 0 ||
		pipelineInfo["succeeded"].(float64) != 0 ||
		pipelineInfo["failed"].(float64) != 0 {
		t.Fatalf("expected zero pipeline counts in empty status, got %#v", pipelineInfo)
	}
	buildInfo, ok := status["build"].(map[string]any)
	if !ok {
		t.Fatalf("expected build info in status")
	}
	if version, ok := buildInfo["version"].(string); !ok || version != buildinfo.Version {
		t.Fatalf("unexpected build version: %#v", buildInfo["version"])
	}

	licenseInfo, ok := status["license"].(map[string]any)
	if !ok {
		t.Fatalf("expected license info in status")
	}
	if mode, ok := licenseInfo["mode"].(string); !ok || mode != "enterprise" {
		t.Fatalf("unexpected license mode: %#v", licenseInfo["mode"])
	}
}

func TestHandleStatusPipelineAggregationByTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	type jobSeed struct {
		id     string
		tenant string
		state  model.JobState
	}
	seeds := []jobSeed{
		{id: "job-pending", tenant: "default", state: model.JobStatePending},
		{id: "job-approval", tenant: "default", state: model.JobStateApproval},
		{id: "job-scheduled", tenant: "default", state: model.JobStateScheduled},
		{id: "job-dispatched", tenant: "default", state: model.JobStateDispatched},
		{id: "job-running", tenant: "default", state: model.JobStateRunning},
		{id: "job-succeeded", tenant: "default", state: model.JobStateSucceeded},
		{id: "job-failed", tenant: "default", state: model.JobStateFailed},
		{id: "job-denied", tenant: "default", state: model.JobStateDenied},
		{id: "job-other-tenant", tenant: "other", state: model.JobStateRunning},
	}

	seedState := func(jobID string, target model.JobState) error {
		switch target {
		case model.JobStateSucceeded:
			if err := s.jobStore.SetState(ctx, jobID, model.JobStateRunning); err != nil {
				return err
			}
			return s.jobStore.SetState(ctx, jobID, model.JobStateSucceeded)
		case model.JobStateDenied:
			if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
				return err
			}
			return s.jobStore.SetState(ctx, jobID, model.JobStateDenied)
		default:
			return s.jobStore.SetState(ctx, jobID, target)
		}
	}

	for _, seed := range seeds {
		if err := s.jobStore.SetTenant(ctx, seed.id, seed.tenant); err != nil {
			t.Fatalf("set tenant %s: %v", seed.id, err)
		}
		if err := seedState(seed.id, seed.state); err != nil {
			t.Fatalf("set state %s: %v", seed.id, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var status map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	pipeline, ok := status["pipeline"].(map[string]any)
	if !ok {
		t.Fatalf("expected pipeline in status")
	}
	if got := pipeline["pending"].(float64); got != 3 {
		t.Fatalf("expected pending=3 got=%v", got)
	}
	if got := pipeline["dispatched"].(float64); got != 1 {
		t.Fatalf("expected dispatched=1 got=%v", got)
	}
	if got := pipeline["running"].(float64); got != 1 {
		t.Fatalf("expected running=1 got=%v", got)
	}
	if got := pipeline["succeeded"].(float64); got != 1 {
		t.Fatalf("expected succeeded=1 got=%v", got)
	}
	if got := pipeline["failed"].(float64); got != 1 {
		t.Fatalf("expected failed=1 got=%v", got)
	}
	if got := pipeline["denied"].(float64); got != 1 {
		t.Fatalf("expected denied=1 got=%v", got)
	}
}

func TestHandleStatusWithNatsBus(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.bus = &bus.NatsBus{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}
	var status map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if _, ok := status["nats"].(map[string]any); !ok {
		t.Fatalf("expected nats status")
	}
}

func TestHandleWorkersFiltersStaleEntries(t *testing.T) {
	s, _, _ := newTestGateway(t)
	now := time.Now().UTC()

	s.workerMu.Lock()
	s.workers["fresh"] = &pb.Heartbeat{WorkerId: "fresh"}
	s.workers["stale"] = &pb.Heartbeat{WorkerId: "stale"}
	s.workerSeen["fresh"] = now
	s.workerSeen["stale"] = now.Add(-(workerHeartbeatTTL + time.Second))
	s.workerMu.Unlock()

	workersReq := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
	workersReq.Header.Set("X-Tenant-ID", "default")
	workersRec := httptest.NewRecorder()
	s.handleGetWorkers(workersRec, workersReq)
	if workersRec.Code != http.StatusOK {
		t.Fatalf("unexpected workers status: %d", workersRec.Code)
	}
	var workersResp struct{ Items []*pb.Heartbeat }
	if err := json.NewDecoder(workersRec.Body).Decode(&workersResp); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(workersResp.Items) != 1 || workersResp.Items[0].WorkerId != "fresh" {
		t.Fatalf("expected only fresh worker, got %+v", workersResp.Items)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	statusReq.Header.Set("X-Tenant-ID", "default")
	statusRec := httptest.NewRecorder()
	s.handleStatus(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", statusRec.Code)
	}
	var status map[string]any
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	workersInfo, ok := status["workers"].(map[string]any)
	if !ok || workersInfo["count"].(float64) != 1 {
		t.Fatalf("expected workers count=1, got %#v", status["workers"])
	}
}

func TestHandleGetWorkersFromRedisSnapshot(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Populate Redis with a worker snapshot (2 workers).
	snap := registry.Snapshot{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Workers: []registry.WorkerSummary{
			{WorkerID: "snap-w1", Pool: "pool-a", ActiveJobs: 1, MaxParallelJobs: 4},
			{WorkerID: "snap-w2", Pool: "pool-b", ActiveJobs: 0, MaxParallelJobs: 2, Capabilities: []string{"gpu"}},
		},
	}
	snapJSON, _ := json.Marshal(snap)
	if err := s.memStore.PutResult(context.Background(), registry.SnapshotKey, snapJSON); err != nil {
		t.Fatalf("put snapshot: %v", err)
	}

	// Also add a local in-memory worker to verify snapshot takes priority.
	s.workerMu.Lock()
	s.workers["local-w"] = &pb.Heartbeat{WorkerId: "local-w"}
	s.workerMu.Unlock()

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
	rec := httptest.NewRecorder()
	s.handleGetWorkers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var resp struct{ Items []*pb.Heartbeat }
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 workers from snapshot, got %d", len(resp.Items))
	}
	ids := map[string]bool{}
	for _, w := range resp.Items {
		ids[w.WorkerId] = true
	}
	if !ids["snap-w1"] || !ids["snap-w2"] {
		t.Fatalf("expected snap-w1 and snap-w2, got %v", ids)
	}
}

func TestHandleGetWorkersFallbackOnRedisError(t *testing.T) {
	// Use stubMemStore that returns error on GetResult.
	s := &server{
		memStore:   &errorMemStore{},
		workers:    map[string]*pb.Heartbeat{"local-w1": {WorkerId: "local-w1"}},
		workerSeen: map[string]time.Time{"local-w1": time.Now()},
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
	rec := httptest.NewRecorder()
	s.handleGetWorkers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var resp struct{ Items []*pb.Heartbeat }
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].WorkerId != "local-w1" {
		t.Fatalf("expected fallback to in-memory worker, got %+v", resp.Items)
	}
}

func TestHandleGetWorkersColdStart(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Populate Redis snapshot with 3 workers, but leave in-memory workers empty.
	snap := registry.Snapshot{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Workers: []registry.WorkerSummary{
			{WorkerID: "w1", Pool: "pool-a"},
			{WorkerID: "w2", Pool: "pool-a"},
			{WorkerID: "w3", Pool: "pool-b"},
		},
	}
	snapJSON, _ := json.Marshal(snap)
	if err := s.memStore.PutResult(context.Background(), registry.SnapshotKey, snapJSON); err != nil {
		t.Fatalf("put snapshot: %v", err)
	}
	// In-memory workers is empty (simulating cold start).

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil))
	rec := httptest.NewRecorder()
	s.handleGetWorkers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var resp struct{ Items []*pb.Heartbeat }
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 workers from snapshot on cold start, got %d", len(resp.Items))
	}
}

func TestHandleStatusWorkerCountFromSnapshot(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Populate Redis snapshot with 2 workers.
	snap := registry.Snapshot{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Workers: []registry.WorkerSummary{
			{WorkerID: "snap-w1", Pool: "pool-a"},
			{WorkerID: "snap-w2", Pool: "pool-b"},
		},
	}
	snapJSON, _ := json.Marshal(snap)
	if err := s.memStore.PutResult(context.Background(), registry.SnapshotKey, snapJSON); err != nil {
		t.Fatalf("put snapshot: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var status map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	workersInfo, ok := status["workers"].(map[string]any)
	if !ok || workersInfo["count"].(float64) != 2 {
		t.Fatalf("expected workers count=2 from snapshot, got %#v", status["workers"])
	}
}

// errorMemStore returns errors on all operations, used to test fallback paths.
type errorMemStore struct{}

func (e *errorMemStore) PutContext(context.Context, string, []byte) error {
	return fmt.Errorf("store unavailable")
}
func (e *errorMemStore) DeleteContext(context.Context, string) error {
	return fmt.Errorf("store unavailable")
}
func (e *errorMemStore) GetContext(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("store unavailable")
}
func (e *errorMemStore) PutResult(context.Context, string, []byte) error {
	return fmt.Errorf("store unavailable")
}
func (e *errorMemStore) GetResult(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("store unavailable")
}
func (e *errorMemStore) Close() error { return nil }

func TestHandleStatusHAFields(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.instanceID = "gw-test-123"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}
	var status map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}

	// instance_id
	if id, ok := status["instance_id"].(string); !ok || id != "gw-test-123" {
		t.Fatalf("expected instance_id=gw-test-123, got %v", status["instance_id"])
	}

	// rate_limiter
	rl, ok := status["rate_limiter"].(map[string]any)
	if !ok {
		t.Fatalf("expected rate_limiter in status")
	}
	if mode, ok := rl["mode"].(string); !ok || mode != "memory" {
		t.Fatalf("expected rate_limiter.mode=memory (no redis RL in test), got %v", rl["mode"])
	}

	// circuit_breakers
	cb, ok := status["circuit_breakers"].(map[string]any)
	if !ok {
		t.Fatalf("expected circuit_breakers in status")
	}
	inputCB, ok := cb["input"].(map[string]any)
	if !ok {
		t.Fatalf("expected circuit_breakers.input")
	}
	// With miniredis and no failures written, state should be CLOSED.
	if state := inputCB["state"].(string); state != "CLOSED" {
		t.Fatalf("expected input CB state=CLOSED, got %s", state)
	}
	if failures := inputCB["failures"].(float64); failures != 0 {
		t.Fatalf("expected input CB failures=0, got %v", failures)
	}
	outputCB, ok := cb["output"].(map[string]any)
	if !ok {
		t.Fatalf("expected circuit_breakers.output")
	}
	if state := outputCB["state"].(string); state != "CLOSED" {
		t.Fatalf("expected output CB state=CLOSED, got %s", state)
	}

	// Backward compat: existing fields still present.
	if _, ok := status["build"].(map[string]any); !ok {
		t.Fatal("expected build info still present")
	}
	if _, ok := status["workers"].(map[string]any); !ok {
		t.Fatal("expected workers info still present")
	}
	if _, ok := status["pipeline"].(map[string]any); !ok {
		t.Fatal("expected pipeline info still present")
	}
}

func TestHandleStatusHAFieldsWithCircuitBreakerOpen(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.instanceID = "gw-cb-test"
	ctx := context.Background()

	// Simulate an open circuit breaker by writing failures to Redis.
	rdb := s.jobStore.Client()
	rdb.Set(ctx, "cordum:cb:safety:failures", "5", 30*time.Second)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}
	var status map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}

	cb := status["circuit_breakers"].(map[string]any)
	inputCB := cb["input"].(map[string]any)
	if state := inputCB["state"].(string); state != "OPEN" {
		t.Fatalf("expected input CB state=OPEN with 5 failures, got %s", state)
	}
	if failures := inputCB["failures"].(float64); failures != 5 {
		t.Fatalf("expected failures=5, got %v", failures)
	}
	if cooldown := inputCB["cooldown_remaining_ms"].(float64); cooldown <= 0 {
		t.Fatalf("expected positive cooldown, got %v", cooldown)
	}

	// Output CB should still be CLOSED (no failures written).
	outputCB := cb["output"].(map[string]any)
	if state := outputCB["state"].(string); state != "CLOSED" {
		t.Fatalf("expected output CB state=CLOSED, got %s", state)
	}
}

func TestHandleStatusHAFieldsWithReplicas(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.instanceID = "gw-replica-test"
	ctx := context.Background()

	// Register a fake instance in Redis.
	rdb := s.jobStore.Client()
	instReg := registry.NewInstanceRegistry(rdb, "api-gateway", "gw-1", "v0.2.0", "abc123")
	instReg.Start(ctx)
	defer instReg.Stop()

	// The gateway also needs an instanceRegistry to trigger the replicas section.
	s.instanceRegistry = registry.NewInstanceRegistry(rdb, "api-gateway", "gw-replica-test", "v0.2.0", "def456")
	s.instanceRegistry.Start(ctx)
	defer s.instanceRegistry.Stop()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}
	var status map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}

	replicas, ok := status["replicas"].(map[string]any)
	if !ok {
		t.Fatalf("expected replicas map in status, got %T: %v", status["replicas"], status["replicas"])
	}
	gwReplicas, ok := replicas["api-gateway"].([]any)
	if !ok {
		t.Fatalf("expected api-gateway replicas array, got %T", replicas["api-gateway"])
	}
	if len(gwReplicas) != 2 {
		t.Fatalf("expected 2 api-gateway replicas, got %d", len(gwReplicas))
	}
}

func TestHandleStatusHAFieldsNilRegistry(t *testing.T) {
	// Verify no crash when instanceRegistry is nil (single-replica mode).
	s := &server{
		memStore:   &errorMemStore{},
		workers:    map[string]*pb.Heartbeat{},
		workerSeen: map[string]time.Time{},
		instanceID: "standalone",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}
	var status map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// instance_id should be present.
	if status["instance_id"] != "standalone" {
		t.Fatalf("expected instance_id=standalone, got %v", status["instance_id"])
	}
	// replicas should be absent (no registry).
	if _, ok := status["replicas"]; ok {
		t.Fatal("expected replicas to be absent when registry is nil")
	}
	// circuit_breakers should show UNKNOWN (nil Redis).
	cb := status["circuit_breakers"].(map[string]any)
	inputCB := cb["input"].(map[string]any)
	if state := inputCB["state"].(string); state != "UNKNOWN" {
		t.Fatalf("expected UNKNOWN CB state with nil Redis, got %s", state)
	}
}
