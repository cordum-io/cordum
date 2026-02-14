package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
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
