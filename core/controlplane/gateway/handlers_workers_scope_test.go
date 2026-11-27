package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	workerScopeA      = "w-scope-a"
	workerScopeB      = "w-scope-b"
	workerScopeOrphan = "w-scope-orphan"
)

func workerScopeSeed(t *testing.T, sw *crossTenantSweep) {
	t.Helper()
	seedWorkerCredentialAccessConfig(t, sw.server)
	for i, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
		id := []string{workerScopeA, workerScopeB}[i]
		body := fmt.Sprintf(`{"worker_id":%q,"allowed_pools":["default"],"allowed_topics":["job.external"]}`, id)
		rr := sw.as(t, who, http.MethodPost, "/api/v1/workers/credentials", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("credential status=%d", rr.Code)
		}
	}
	snap := registry.Snapshot{CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Topics: map[string]registry.TopicSnapshot{"job.external": {Pool: "default"}}}
	for _, id := range []string{workerScopeA, workerScopeB, workerScopeOrphan} {
		snap.Workers = append(snap.Workers, registry.WorkerSummary{
			WorkerID: id, Pool: "default", Capabilities: []string{id + "-cap"},
		})
	}
	seedSnapshot(t, sw.server, snap)
	ctx := context.Background()
	for i, tenant := range []string{sw.tenantA.tenantID, " " + sw.tenantB.tenantID + " ", ""} {
		id := []string{"worker-scope-job-a", "worker-scope-job-b", "worker-scope-job-empty"}[i]
		if tenant != "" {
			if err := sw.server.jobStore.SetTenant(ctx, id, tenant); err != nil {
				t.Fatal(err)
			}
		}
		for _, err := range []error{
			sw.server.jobStore.SetState(ctx, id, model.JobStatePending),
			sw.server.jobStore.SetTopic(ctx, id, "job.external"),
			sw.server.jobStore.SetWorkerID(ctx, id, workerScopeB),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func workerScopeResponse(t *testing.T, rr *httptest.ResponseRecorder, status int, present string, absent ...string) {
	t.Helper()
	if rr.Code != status {
		t.Errorf("status=%d, want %d; body=%s", rr.Code, status, rr.Body.String())
	}
	if present != "" && !strings.Contains(rr.Body.String(), present) {
		t.Errorf("response missing %q: %s", present, rr.Body.String())
	}
	for _, marker := range absent {
		if strings.Contains(rr.Body.String(), marker) {
			t.Errorf("response contains excluded marker %q", marker)
		}
	}
}

func workerScopeDirect(s *server, ac *auth.AuthContext, id, suffix string) *httptest.ResponseRecorder {
	r := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil), ac)
	r.SetPathValue("id", id)
	r.Header.Set("X-Tenant-ID", "header-does-not-select-scope")
	rr := httptest.NewRecorder()
	switch {
	case suffix == "/jobs":
		s.handleGetWorkerJobs(rr, r)
	case id != "":
		s.handleGetWorker(rr, r)
	default:
		s.handleGetWorkers(rr, r)
	}
	return rr
}

func workerScopeLists(t *testing.T, sw *crossTenantSweep) {
	t.Helper()
	for i, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
		own, other := workerScopeA, workerScopeB
		if i == 1 {
			own, other = other, own
		}
		rr := sw.as(t, who, http.MethodGet, "/api/v1/workers", "")
		workerScopeResponse(t, rr, http.StatusOK, own+"-cap", other, workerScopeOrphan, "worker-scope-job-")
	}
}

func workerScopeDetails(t *testing.T, sw *crossTenantSweep) {
	t.Helper()
	for i, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
		own, other := workerScopeA, workerScopeB
		if i == 1 {
			own, other = other, own
		}
		rr := sw.as(t, who, http.MethodGet, "/api/v1/workers/"+own, "")
		workerScopeResponse(t, rr, http.StatusOK, own+"-cap", other, workerScopeOrphan, "worker-scope-job-")
		for _, suffix := range []string{"", "/jobs"} {
			unknown := sw.as(t, who, http.MethodGet, "/api/v1/workers/unknown"+suffix, "")
			for _, id := range []string{other, workerScopeOrphan} {
				rr := sw.as(t, who, http.MethodGet, "/api/v1/workers/"+id+suffix, "")
				workerScopeResponse(t, rr, http.StatusNotFound, "WORKER_NOT_FOUND", other, workerScopeOrphan, "worker-scope-job-")
				if rr.Body.String() != unknown.Body.String() || rr.Code != unknown.Code {
					t.Error("worker response differs from unknown id")
				}
			}
		}
	}
}

func TestWorkerReadsScopedToCredentialTenant(t *testing.T) {
	sw := newCrossTenantSweep(t)
	workerScopeSeed(t, sw)
	t.Run("list", func(t *testing.T) { workerScopeLists(t, sw) })
	t.Run("get", func(t *testing.T) { workerScopeDetails(t, sw) })
	t.Run("jobs", func(t *testing.T) {
		for i, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
			id, own, other := workerScopeA, "worker-scope-job-a", "worker-scope-job-b"
			if i == 1 {
				id, own, other = workerScopeB, other, own
			}
			rr := sw.as(t, who, http.MethodGet, "/api/v1/workers/"+id+"/jobs", "")
			workerScopeResponse(t, rr, http.StatusOK, own, other, "worker-scope-job-empty", workerScopeOrphan+"-cap")
		}
	})
	t.Run("fallback-list", func(t *testing.T) { workerScopeFallback(t, sw) })
	t.Run("no-tenant-context", func(t *testing.T) { workerScopeEmptyTenant(t, sw) })
	t.Run("cross-tenant", func(t *testing.T) { workerScopeUnscoped(t, sw) })
	t.Run("store-unavailable", func(t *testing.T) { workerScopeUnavailable(t, sw) })
}

func workerScopeFallback(t *testing.T, sw *crossTenantSweep) {
	t.Helper()
	s := sw.server
	mem := s.memStore
	s.memStore = &errorMemStore{}
	s.workerMu.Lock()
	workers, seen := s.workers, s.workerSeen
	s.workers, s.workerSeen = map[string]*pb.Heartbeat{}, nil
	for _, id := range []string{workerScopeA, workerScopeB, workerScopeOrphan} {
		s.workers[id] = &pb.Heartbeat{WorkerId: id, Pool: "default", Capabilities: []string{id + "-cap"}}
	}
	s.workerMu.Unlock()
	defer func() {
		s.memStore = mem
		s.workerMu.Lock()
		s.workers, s.workerSeen = workers, seen
		s.workerMu.Unlock()
	}()
	workerScopeLists(t, sw)
	workerScopeDetails(t, sw)
}

func workerScopeEmptyTenant(t *testing.T, sw *crossTenantSweep) {
	t.Helper()
	s := sw.server
	store := s.workerCredentialStore
	defer func() { s.workerCredentialStore = store }()
	for _, credentials := range []*workercredentials.Service{store, nil} {
		s.workerCredentialStore = credentials
		for _, tenant := range []string{"", " \t "} {
			ac := &auth.AuthContext{Role: "admin", Tenant: tenant}
			rr := workerScopeDirect(s, ac, "", "")
			workerScopeResponse(t, rr, http.StatusOK, `"items":[]`, workerScopeA, workerScopeB, workerScopeOrphan, "worker-scope-job-")
			for _, suffix := range []string{"", "/jobs"} {
				rr = workerScopeDirect(s, ac, workerScopeB, suffix)
				workerScopeResponse(t, rr, http.StatusNotFound, "WORKER_NOT_FOUND", workerScopeA, workerScopeB, workerScopeOrphan, "worker-scope-job-")
			}
		}
	}
}

func workerScopeUnscoped(t *testing.T, sw *crossTenantSweep) {
	t.Helper()
	s := sw.server
	provider, store := s.auth, s.workerCredentialStore
	defer func() { s.auth, s.workerCredentialStore = provider, store }()
	s.workerCredentialStore = nil
	for _, disabled := range []bool{false, true} {
		ac := &auth.AuthContext{Role: "admin", Tenant: sw.tenantA.tenantID, AllowCrossTenant: true}
		if disabled {
			s.auth, ac = nil, nil
		}
		rr := workerScopeDirect(s, ac, "", "")
		for _, id := range []string{workerScopeA, workerScopeB, workerScopeOrphan} {
			workerScopeResponse(t, rr, http.StatusOK, id+"-cap")
			workerScopeResponse(t, workerScopeDirect(s, ac, id, ""), http.StatusOK, id+"-cap")
		}
		rr = workerScopeDirect(s, ac, workerScopeB, "/jobs")
		for _, id := range []string{"worker-scope-job-a", "worker-scope-job-b", "worker-scope-job-empty"} {
			workerScopeResponse(t, rr, http.StatusOK, id)
		}
	}
}

func workerScopeUnavailable(t *testing.T, sw *crossTenantSweep) {
	t.Helper()
	s := sw.server
	store := s.workerCredentialStore
	defer func() { s.workerCredentialStore = store }()
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable} {
		s.workerCredentialStore = &workercredentials.Service{}
		if status == http.StatusServiceUnavailable {
			s.workerCredentialStore = nil
		}
		for _, path := range []string{"", "/" + workerScopeB, "/" + workerScopeB + "/jobs"} {
			rr := sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/workers"+path, "")
			workerScopeResponse(t, rr, status, "", workerScopeA, workerScopeB, workerScopeOrphan, "worker-scope-job-")
		}
	}
}
