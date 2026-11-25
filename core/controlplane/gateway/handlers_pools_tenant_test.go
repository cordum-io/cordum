package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/registry"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"github.com/google/uuid"
)

var poolPlatformAdmin = crossTenantSweepTenant{"tenant-pool-platform", "pool-platform-admin-key", "principal-pool-platform"}

type poolPlatformMarkers struct{ description, capability string }

type poolPlatformRequest struct{ method, path, body string }

// poolPlatformRefusals are tenant-bound requests to each pool route. Each
// subtest seeds pool-b with the given status, so a write that is not refused
// would succeed and change the pools data.
var poolPlatformRefusals = []struct {
	name, status string
	requests     []poolPlatformRequest
}{
	{"list", "", []poolPlatformRequest{{http.MethodGet, "/api/v1/pools", ""}}},
	{"get", "", []poolPlatformRequest{{http.MethodGet, "/api/v1/pools/pool-b", ""}}},
	{"create", "", []poolPlatformRequest{
		{http.MethodPut, "/api/v1/pools/poolplatform-free", `{"description":"poolplatform-new"}`},
		{http.MethodPut, "/api/v1/pools/pool-b", `{"description":"poolplatform-new"}`},
	}},
	{"update", "", []poolPlatformRequest{{http.MethodPatch, "/api/v1/pools/pool-b", `{"description":"poolplatform-changed"}`}}},
	{"delete", config.PoolStatusInactive, []poolPlatformRequest{{http.MethodDelete, "/api/v1/pools/pool-b?force=true", ""}}},
	{"drain", "", []poolPlatformRequest{{http.MethodPost, "/api/v1/pools/pool-b/drain", `{"timeout_seconds":60}`}}},
	{"add-topic", "", []poolPlatformRequest{{http.MethodPut, "/api/v1/pools/pool-b/topics/job.poolplatform.extra", ""}}},
	{"remove-topic", "", []poolPlatformRequest{{http.MethodDelete, "/api/v1/pools/pool-b/topics/job.pool-b", ""}}},
}

func TestPoolRoutesPlatformOnly(t *testing.T) {
	for _, tc := range poolPlatformRefusals {
		t.Run(tc.name, func(t *testing.T) {
			sw, m := poolPlatformSweep(t, tc.status)
			before, mark := poolPlatformPools(t, sw), sw.publishMark()
			for _, req := range tc.requests {
				for _, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
					label := fmt.Sprintf("%s %s as %s", req.method, req.path, who.tenantID)
					rr := sw.as(t, who, req.method, req.path, req.body)
					assertCrossTenantRefused(t, rr, label, m.description, m.capability)
					if rr.Code != http.StatusForbidden {
						t.Fatalf("%s: status=%d, want 403", label, rr.Code)
					}
				}
			}
			poolPlatformUnchanged(t, sw, before, mark)
		})
	}
	t.Run("empty-tenant", func(t *testing.T) {
		sw, _ := poolPlatformSweep(t, "")
		before, mark := poolPlatformPools(t, sw), sw.publishMark()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/pools/pool-b", strings.NewReader(`{"description":"poolplatform-changed"}`))
		req.Header.Set("Content-Type", "application/json")
		req = withAuth(withPathValues(req, "name", "pool-b"), &auth.AuthContext{Role: "admin", PrincipalID: "poolplatform-tenantless"})
		rr := httptest.NewRecorder()
		sw.server.handleUpdatePool(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("tenantless update: status=%d, want 403; body=%s", rr.Code, rr.Body.String())
		}
		poolPlatformUnchanged(t, sw, before, mark)
	})
	t.Run("platform", poolPlatformAllowed)
}

func poolPlatformAllowed(t *testing.T) {
	sw, m := poolPlatformSweep(t, "")
	for path, want := range map[string]string{"/api/v1/pools": `"pool-b"`, "/api/v1/pools/pool-b": m.capability} {
		rr := sw.as(t, poolPlatformAdmin, http.MethodGet, path, "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("GET %s: status=%d, want 200 with %q; body=%s", path, rr.Code, want, rr.Body.String())
		}
	}
	// The system/default write keeps topic keys it is not given, so remove-topic
	// takes pool-b off a topic that also maps to poolplatform-free: the stored
	// value changes from two pools to one.
	for _, step := range []struct {
		poolPlatformRequest
		code int
	}{
		{poolPlatformRequest{http.MethodPatch, "/api/v1/pools/pool-b", `{"description":"poolplatform-changed"}`}, http.StatusOK},
		{poolPlatformRequest{http.MethodPut, "/api/v1/pools/poolplatform-free", `{"description":"poolplatform-new"}`}, http.StatusCreated},
		{poolPlatformRequest{http.MethodPut, "/api/v1/pools/poolplatform-free/topics/job.pool-b", ""}, http.StatusNoContent},
		{poolPlatformRequest{http.MethodDelete, "/api/v1/pools/pool-b/topics/job.pool-b", ""}, http.StatusNoContent},
		{poolPlatformRequest{http.MethodPost, "/api/v1/pools/pool-b/drain", `{"timeout_seconds":60}`}, http.StatusOK},
		{poolPlatformRequest{http.MethodPatch, "/api/v1/pools/poolplatform-free", `{"status":"inactive"}`}, http.StatusOK},
		{poolPlatformRequest{http.MethodDelete, "/api/v1/pools/poolplatform-free?force=true", ""}, http.StatusNoContent},
	} {
		label := step.method + " " + step.path
		before, mark := poolPlatformPools(t, sw), sw.publishMark()
		rr := sw.as(t, poolPlatformAdmin, step.method, step.path, step.body)
		if rr.Code != step.code {
			t.Fatalf("%s: status=%d, want %d; body=%s", label, rr.Code, step.code, rr.Body.String())
		}
		if poolPlatformPools(t, sw) == before {
			t.Fatalf("%s: pools data unchanged", label)
		}
		if got := sw.publishedSince(mark); len(got) != 1 || got[0].subject != capsdk.SubjectConfigChanged {
			t.Fatalf("%s: published %v, want one %s message", label, got, capsdk.SubjectConfigChanged)
		}
	}
}

// poolPlatformSweep is the sweep server with a third API key whose access
// spans all tenants. pool-b, its topic and one worker are seeded through the
// stores, never through a route.
func poolPlatformSweep(t *testing.T, status string) (*crossTenantSweep, poolPlatformMarkers) {
	t.Helper()
	sw := newCrossTenantSweep(t)
	s := sw.server
	a, b, p := sw.tenantA, sw.tenantB, poolPlatformAdmin
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": fmt.Sprintf(`[{"key":%q,"tenant":%q,"role":"admin","principal_id":%q},{"key":%q,"tenant":%q,"role":"admin","principal_id":%q},{"key":%q,"tenant":%q,"role":"admin","principal_id":%q,"allow_cross_tenant":true}]`,
			a.apiKey, a.tenantID, a.principalID, b.apiKey, b.tenantID, b.principalID, p.apiKey, p.tenantID, p.principalID),
	})
	sw.handler = apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(sw.mux, s.entitlements)))
	m := poolPlatformMarkers{"poolplatform-desc-" + uuid.NewString(), "poolplatform-cap-" + uuid.NewString()}
	seedPoolConfig(t, s, map[string]any{"job.pool-b": "pool-b"}, map[string]config.PoolConfig{
		"pool-b": {Status: status, Requires: []string{}, Description: m.description},
	})
	seedSnapshot(t, s, registry.Snapshot{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Pools:      map[string]registry.PoolSnapshot{"pool-b": {Workers: 1, Capacity: 4}},
		Topics:     map[string]registry.TopicSnapshot{"job.pool-b": {Pool: "pool-b", Workers: 1, Capacity: 4, Available: true}},
		Workers:    []registry.WorkerSummary{{WorkerID: "poolplatform-worker-b", Pool: "pool-b", MaxParallelJobs: 4, Capabilities: []string{m.capability}}},
	})
	return sw, m
}

// poolPlatformPools returns the system/default pools data as JSON.
func poolPlatformPools(t *testing.T, sw *crossTenantSweep) string {
	t.Helper()
	doc, err := sw.server.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("read pools config: %v", err)
	}
	data, err := json.Marshal(doc.Data["pools"])
	if err != nil {
		t.Fatalf("marshal pools config: %v", err)
	}
	return string(data)
}

func poolPlatformUnchanged(t *testing.T, sw *crossTenantSweep, before string, mark int) {
	t.Helper()
	if after := poolPlatformPools(t, sw); after != before {
		t.Errorf("pools data changed:\nbefore=%s\nafter=%s", before, after)
	}
	if got := sw.publishedSince(mark); len(got) != 0 {
		t.Errorf("published %d messages, want none: %v", len(got), got)
	}
}
