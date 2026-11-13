package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
)

// statusAdminOnlyFields are the admin block's top-level fields that are
// always present for an admin caller.
var statusAdminOnlyFields = []string{"instance_id", "rate_limiter", "circuit_breakers", "signature_enforcement", "ha_env"}

// statusConditionalAdminFields are admin-only fields that appear only when
// their data source is available. A non-admin response must never carry them.
var statusConditionalAdminFields = []string{"input_fail_open_total", "snapshot_meta", "replicas"}

// statusCacheScopeServer returns a test gateway with the status cache on.
// newTestGateway leaves it off, so these tests set a long TTL explicitly.
func statusCacheScopeServer(t *testing.T) *server {
	t.Helper()
	sigStatusEnv(t, "")
	s, _, _ := newTestGateway(t)
	s.instanceID = "gw-scope-1"
	s.statusCacheObj = newStatusCache(time.Minute)
	return s
}

// statusAs sends one status request as role in tenant and returns the body
// and the X-Cache header.
func statusAs(t *testing.T, s *server, role, tenant string) (map[string]any, string) {
	t.Helper()
	s.auth = &tenantStrictAuth{tenant: tenant, role: role}
	authCtx := &auth.AuthContext{Tenant: tenant, Role: role, PrincipalID: role + "-1"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Tenant-ID", tenant)
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp, rec.Header().Get("X-Cache")
}

func assertStatusAdminView(t *testing.T, label string, resp map[string]any) {
	t.Helper()
	for _, field := range statusAdminOnlyFields {
		if _, ok := resp[field]; !ok {
			t.Fatalf("%s: admin response lacks %q", label, field)
		}
	}
	if nats, _ := resp["nats"].(map[string]any); nats == nil {
		t.Fatalf("%s: admin response lacks nats", label)
	} else if _, ok := nats["url"]; !ok {
		t.Fatalf("%s: admin response lacks nats.url", label)
	}
}

func assertStatusMemberView(t *testing.T, label string, resp map[string]any) {
	t.Helper()
	for _, field := range append(append([]string{}, statusAdminOnlyFields...), statusConditionalAdminFields...) {
		if v, ok := resp[field]; ok {
			t.Fatalf("%s: non-admin response carries admin-only %q: %#v", label, field, v)
		}
	}
	if nats, _ := resp["nats"].(map[string]any); nats != nil {
		if _, ok := nats["url"]; ok {
			t.Fatalf("%s: non-admin response carries nats.url", label)
		}
	}
	if redisInfo, _ := resp["redis"].(map[string]any); redisInfo != nil {
		if _, ok := redisInfo["error"]; ok {
			t.Fatalf("%s: non-admin response carries redis.error", label)
		}
	}
	for _, field := range []string{"time", "build", "workers", "pipeline"} {
		if _, ok := resp[field]; !ok {
			t.Fatalf("%s: non-admin response lacks %q", label, field)
		}
	}
}

func TestHandleStatusCache_ViewerThenAdmin(t *testing.T) {
	s := statusCacheScopeServer(t)

	viewer, cache := statusAs(t, s, "viewer", "default")
	if cache != "MISS" {
		t.Fatalf("first viewer request X-Cache = %q, want MISS", cache)
	}
	assertStatusMemberView(t, "viewer", viewer)

	admin, cache := statusAs(t, s, "admin", "default")
	if cache != "MISS" {
		t.Fatalf("admin request after a viewer request X-Cache = %q, want MISS (served the viewer's entry)", cache)
	}
	assertStatusAdminView(t, "admin", admin)

	viewerAgain, cache := statusAs(t, s, "viewer", "default")
	if cache != "HIT" {
		t.Fatalf("second viewer request X-Cache = %q, want HIT", cache)
	}
	assertStatusMemberView(t, "viewer (cached)", viewerAgain)
}

func TestHandleStatusCache_AdminThenViewer(t *testing.T) {
	s := statusCacheScopeServer(t)

	admin, cache := statusAs(t, s, "admin", "default")
	if cache != "MISS" {
		t.Fatalf("first admin request X-Cache = %q, want MISS", cache)
	}
	assertStatusAdminView(t, "admin", admin)

	viewer, cache := statusAs(t, s, "viewer", "default")
	if cache != "MISS" {
		t.Fatalf("viewer request after an admin request X-Cache = %q, want MISS (served the admin's entry)", cache)
	}
	assertStatusMemberView(t, "viewer", viewer)

	adminAgain, cache := statusAs(t, s, "admin", "default")
	if cache != "HIT" {
		t.Fatalf("second admin request X-Cache = %q, want HIT", cache)
	}
	assertStatusAdminView(t, "admin (cached)", adminAgain)
}

func TestHandleStatusCache_TenantsDoNotShareEntries(t *testing.T) {
	s := statusCacheScopeServer(t)
	ctx := context.Background()
	if err := s.jobStore.SetTenant(ctx, "job-other-running", "other"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if err := s.jobStore.SetState(ctx, "job-other-running", model.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}

	running := func(resp map[string]any) float64 {
		t.Helper()
		pipeline, ok := resp["pipeline"].(map[string]any)
		if !ok {
			t.Fatalf("pipeline missing: %#v", resp["pipeline"])
		}
		n, _ := pipeline["running"].(float64)
		return n
	}

	def, _ := statusAs(t, s, "viewer", "default")
	if got := running(def); got != 0 {
		t.Fatalf("tenant default pipeline.running = %v, want 0", got)
	}
	other, cache := statusAs(t, s, "viewer", "other")
	if cache != "MISS" {
		t.Fatalf("tenant other X-Cache = %q, want MISS (served tenant default's entry)", cache)
	}
	if got := running(other); got != 1 {
		t.Fatalf("tenant other pipeline.running = %v, want 1", got)
	}
}
