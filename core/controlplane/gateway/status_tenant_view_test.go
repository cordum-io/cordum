package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestStatusShowsOnlyTheCallersTenant(t *testing.T) {
	t.Run("tenant-cache", statusViewTenantCache)
	t.Run("invalidate", statusViewInvalidate)
	for _, mode := range []string{"gateway", "spanning", "viewer"} {
		t.Run(mode, func(t *testing.T) { statusViewPlatformCache(t, mode) })
	}
	t.Run("empty-tenant", func(t *testing.T) {
		sw, _, _ := statusViewSeed(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{Role: "admin"}))
		rr := httptest.NewRecorder()
		sw.server.handleStatus(rr, req)
		statusViewCheck(t, rr, 0, 0, false)
	})
	for _, mode := range []string{"missing", "error", "heartbeat"} {
		t.Run(mode, func(t *testing.T) { statusViewWorkerSource(t, mode) })
	}
}

func statusViewInvalidate(t *testing.T) {
	sw, _, _ := statusViewSeed(t)
	for _, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
		sw.as(t, who, http.MethodGet, "/api/v1/status", "")
	}
	sw.server.statusCacheObj.Invalidate()
	for _, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
		rr := sw.as(t, who, http.MethodGet, "/api/v1/status", "")
		if rr.Code != http.StatusOK || rr.Header().Get("X-Cache") != "MISS" {
			t.Errorf("status after invalidation: code=%d cache=%s", rr.Code, rr.Header().Get("X-Cache"))
		}
	}
}

func statusViewWorkerSource(t *testing.T, mode string) {
	sw, _, _ := statusViewSeed(t)
	want := 0
	switch mode {
	case "missing":
		sw.server.workerCredentialStore = nil
	case "error":
		sw.server.workerCredentialStore = &workercredentials.Service{}
	case "heartbeat":
		sw.server.memStore = nil
		sw.server.workers = map[string]*pb.Heartbeat{
			"status-view-worker": {WorkerId: "status-view-worker"},
			"status-view-other":  {WorkerId: "status-view-other"},
		}
		want = 1
	}
	statusViewCheck(t, sw.as(t, sw.tenantB, http.MethodGet, "/api/v1/status", ""), 1, want, false)
	statusViewCheck(t, sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/status", ""), 0, 0, false)
}

func statusViewTenantCache(t *testing.T) {
	sw, id, marker := statusViewSeed(t)
	before := crossTenantJobMeta(t, sw, id)
	statusViewCheck(t, sw.as(t, sw.tenantB, http.MethodGet, "/api/v1/status", ""), 1, 1, false)
	for _, query := range []string{"tenant", "tenant_id"} {
		rr := sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/status?"+query+"="+sw.tenantB.tenantID, "")
		statusViewCheck(t, rr, 0, 0, false)
		for _, absent := range []string{marker, sw.tenantB.tenantID} {
			if strings.Contains(rr.Body.String(), absent) {
				t.Errorf("response contains %q", absent)
			}
		}
	}
	if !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) {
		t.Error("status read changed owner job")
	}
	rr := sw.as(t, sw.tenantB, http.MethodGet, "/api/v1/status", "")
	statusViewCheck(t, rr, 1, 1, false)
	if rr.Header().Get("X-Cache") != "HIT" {
		t.Error("owner cache entry was not retained")
	}
}

func statusViewPlatformCache(t *testing.T, mode string) {
	sw, _, _ := statusViewSeed(t)
	a, b := sw.tenantA, sw.tenantB
	p := crossTenantSweepTenant{"default", "status-view-platform-key", "status-view-platform"}
	spans := mode == "spanning"
	role := "admin"
	if spans {
		p.tenantID = a.tenantID
	}
	if mode == "viewer" {
		a.tenantID = p.tenantID
		role = "viewer"
	}
	sw.server.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": fmt.Sprintf(`[{"key":%q,"tenant":%q,"role":%q},{"key":%q,"tenant":%q,"role":"admin"},{"key":%q,"tenant":%q,"role":"admin","allow_cross_tenant":%t}]`,
			a.apiKey, a.tenantID, role, b.apiKey, b.tenantID, p.apiKey, p.tenantID, spans),
	})
	sw.handler = apiKeyMiddleware(sw.server.auth, tenantMiddleware(sw.server.auth, maxBodyMiddleware(sw.mux, sw.server.entitlements)))
	statusViewCheck(t, sw.as(t, p, http.MethodGet, "/api/v1/status", ""), 0, 1, true)
	rr := sw.as(t, a, http.MethodGet, "/api/v1/status", "")
	if mode == "viewer" {
		if rr.Code != http.StatusOK {
			t.Fatalf("viewer status=%d", rr.Code)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["instance_id"]; ok {
			t.Error("viewer received admin fields")
		}
	} else {
		statusViewCheck(t, rr, 0, 0, false)
	}
	statusViewCheck(t, sw.as(t, p, http.MethodGet, "/api/v1/status", ""), 0, 1, true)
}

func statusViewSeed(t *testing.T) (*crossTenantSweep, string, string) {
	t.Helper()
	sw := newCrossTenantSweep(t)
	id, marker := seedCrossTenantJob(t, sw)
	ctx := context.Background()
	for _, state := range []model.JobState{model.JobStateDispatched, model.JobStateRunning, model.JobStateSucceeded, model.JobStateFailed, model.JobStateDenied} {
		jobID := "status-view-" + string(state)
		if err := sw.server.jobStore.SetTenant(ctx, jobID, sw.tenantB.tenantID); err != nil {
			t.Fatal(err)
		}
		if state == model.JobStateSucceeded {
			if err := sw.server.jobStore.SetState(ctx, jobID, model.JobStateRunning); err != nil {
				t.Fatal(err)
			}
		}
		if state == model.JobStateDenied {
			if err := sw.server.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
				t.Fatal(err)
			}
		}
		if err := sw.server.jobStore.SetState(ctx, jobID, state); err != nil {
			t.Fatal(err)
		}
	}
	_, err := sw.server.workerCredentialStore.Create(ctx, workercredentials.IssueInput{
		TenantID: sw.tenantB.tenantID, WorkerID: "status-view-worker", CreatedBy: "status-view-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedSnapshot(t, sw.server, registry.Snapshot{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Workers:    []registry.WorkerSummary{{WorkerID: "status-view-worker"}},
	})
	sw.server.statusCacheObj = newStatusCache(time.Minute)
	return sw, id, marker
}

func statusViewCheck(t *testing.T, rr *httptest.ResponseRecorder, jobs, workers int, platform bool) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Pipeline map[string]int       `json:"pipeline"`
		Workers  struct{ Count *int } `json:"workers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"pending", "dispatched", "running", "succeeded", "failed", "denied"} {
		if got, ok := body.Pipeline[field]; !ok || got != jobs {
			t.Errorf("pipeline.%s=%d (present=%t), want %d", field, got, ok, jobs)
		}
	}
	if body.Workers.Count == nil || *body.Workers.Count != workers {
		t.Errorf("workers=%+v, want count %d", body.Workers, workers)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"time", "uptime_seconds", "build", "nats_status", "nats", "redis"} {
		if _, ok := fields[kept]; !ok {
			t.Errorf("missing %s", kept)
		}
	}
	if platform {
		for _, field := range []string{"instance_id", "rate_limiter", "circuit_breakers", "ha_env", "snapshot_meta", "goroutine_count", "active_ws_clients", "redis_pool_stats"} {
			if _, ok := fields[field]; !ok {
				t.Errorf("platform missing %s", field)
			}
		}
		return
	}
	statusViewCheckOmitted(t, fields)
}

func statusViewCheckOmitted(t *testing.T, fields map[string]json.RawMessage) {
	t.Helper()
	for _, field := range []string{"active_ws_clients", "goroutine_count", "redis_pool_stats", "instance_id", "rate_limiter", "circuit_breakers", "input_fail_open_total", "ha_env", "snapshot_meta", "replicas"} {
		if _, ok := fields[field]; ok {
			t.Errorf("tenant view includes %s", field)
		}
	}
	for group, omitted := range map[string]string{"nats": "url", "redis": "error"} {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(fields[group], &nested); err != nil {
			t.Fatal(err)
		}
		if _, ok := nested[omitted]; ok {
			t.Errorf("tenant view includes %s.%s", group, omitted)
		}
	}
}
