package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/registry"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestHandleStatusAndWorkers(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.workerMu.Lock()
	s.workers["w1"] = &pb.Heartbeat{WorkerId: "w1"}
	s.workerMu.Unlock()

	workersReq := httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil)
	workersReq.Header.Set("X-Tenant-ID", "default")
	workersRec := httptest.NewRecorder()
	s.handleGetWorkers(workersRec, workersReq)
	if workersRec.Code != http.StatusOK {
		t.Fatalf("unexpected workers status: %d", workersRec.Code)
	}
	var workers []*pb.Heartbeat
	if err := json.NewDecoder(workersRec.Body).Decode(&workers); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(workers) != 1 || workers[0].WorkerId != "w1" {
		t.Fatalf("unexpected workers list")
	}

	s.auth = stubLicenseAuth{info: &LicenseInfo{Mode: "enterprise", Status: "active", Plan: "Enterprise"}}

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
	if got := pipeline["failed"].(float64); got != 2 {
		t.Fatalf("expected failed=2 got=%v", got)
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

	workersReq := httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil)
	workersReq.Header.Set("X-Tenant-ID", "default")
	workersRec := httptest.NewRecorder()
	s.handleGetWorkers(workersRec, workersReq)
	if workersRec.Code != http.StatusOK {
		t.Fatalf("unexpected workers status: %d", workersRec.Code)
	}
	var workers []*pb.Heartbeat
	if err := json.NewDecoder(workersRec.Body).Decode(&workers); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(workers) != 1 || workers[0].WorkerId != "fresh" {
		t.Fatalf("expected only fresh worker, got %+v", workers)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil)
	rec := httptest.NewRecorder()
	s.handleGetWorkers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var workers []*pb.Heartbeat
	if err := json.NewDecoder(rec.Body).Decode(&workers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(workers) != 2 {
		t.Fatalf("expected 2 workers from snapshot, got %d", len(workers))
	}
	ids := map[string]bool{}
	for _, w := range workers {
		ids[w.WorkerId] = true
	}
	if !ids["snap-w1"] || !ids["snap-w2"] {
		t.Fatalf("expected snap-w1 and snap-w2, got %v", ids)
	}
}

func TestHandleGetWorkersFallbackOnRedisError(t *testing.T) {
	// Use stubMemStore that returns error on GetResult.
	s := &server{
		memStore: &errorMemStore{},
		workers:  map[string]*pb.Heartbeat{"local-w1": {WorkerId: "local-w1"}},
		workerSeen: map[string]time.Time{"local-w1": time.Now()},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil)
	rec := httptest.NewRecorder()
	s.handleGetWorkers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var workers []*pb.Heartbeat
	if err := json.NewDecoder(rec.Body).Decode(&workers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(workers) != 1 || workers[0].WorkerId != "local-w1" {
		t.Fatalf("expected fallback to in-memory worker, got %+v", workers)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil)
	rec := httptest.NewRecorder()
	s.handleGetWorkers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var workers []*pb.Heartbeat
	if err := json.NewDecoder(rec.Body).Decode(&workers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(workers) != 3 {
		t.Fatalf("expected 3 workers from snapshot on cold start, got %d", len(workers))
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

type stubLicenseAuth struct {
	info *LicenseInfo
}

func (s stubLicenseAuth) AuthenticateHTTP(*http.Request) (*AuthContext, error) {
	return &AuthContext{}, nil
}

func (s stubLicenseAuth) AuthenticateGRPC(context.Context) (*AuthContext, error) {
	return &AuthContext{}, nil
}

func (s stubLicenseAuth) RequireRole(*http.Request, ...string) error { return nil }

func (s stubLicenseAuth) ResolveTenant(_ *http.Request, requested, _ string) (string, error) {
	return requested, nil
}

func (s stubLicenseAuth) RequireTenantAccess(*http.Request, string) error { return nil }

func (s stubLicenseAuth) ResolvePrincipal(_ *http.Request, requested string) (string, error) {
	return requested, nil
}

func (s stubLicenseAuth) LicenseInfo() *LicenseInfo { return s.info }
