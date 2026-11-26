package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

func tenantCacheRequest(req *http.Request, tenant, role string) *http.Request {
	req.Header.Set("X-Tenant-ID", tenant)
	return withAuth(req, &auth.AuthContext{Tenant: tenant, Role: role, PrincipalID: "cache-reader"})
}

func tenantCacheBody(t *testing.T, rec *httptest.ResponseRecorder, dest interface{}) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), dest); err != nil {
		t.Fatal(err)
	}
}

func TestTenantIsolationStatusCache(t *testing.T) {
	s := statusCacheScopeServer(t)
	s.auth = governanceAuth{}
	for _, tenant := range []string{"cache-a", "cache-a", "cache-b"} {
		id := fmt.Sprintf("%s-%d", tenant, time.Now().UnixNano())
		if err := s.jobStore.SetTenant(context.Background(), id, tenant); err != nil {
			t.Fatal(err)
		}
		if err := s.jobStore.SetState(context.Background(), id, model.JobStateRunning); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		tenant, role, cache string
		running             int
	}{
		{"cache-a", "viewer", "MISS", 2}, {"cache-a", "viewer", "HIT", 2},
		{"cache-b", "viewer", "MISS", 1}, {"cache-b", "viewer", "HIT", 1},
		{"cache-a", "admin", "MISS", 2}, {"cache-a", "admin", "HIT", 2},
		{"cache-a", "viewer", "HIT", 2},
	} {
		rec := httptest.NewRecorder()
		s.handleStatus(rec, tenantCacheRequest(httptest.NewRequest(http.MethodGet, "/api/v1/status", nil), tc.tenant, tc.role))
		var body struct {
			Pipeline struct{ Running int }
			InstanceID *string `json:"instance_id"`
		}
		tenantCacheBody(t, rec, &body)
		if rec.Header().Get("X-Cache") != tc.cache || body.Pipeline.Running != tc.running {
			t.Fatalf("%+v: cache=%q body=%s", tc, rec.Header().Get("X-Cache"), rec.Body.String())
		}
		if (body.InstanceID != nil) != (tc.role == "admin") {
			t.Fatalf("%+v: wrong admin view: %s", tc, rec.Body.String())
		}
	}
}

func TestTenantIsolationApprovalAnalyticsCache(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.approvalAnalyticsCache = newApprovalAnalyticsCache()
	const since = int64(1_700_000_000_000)
	for i, tenant := range []string{"cache-a", "cache-b", "cache-b"} {
		err := s.decisionLogStore.AppendDecision(context.Background(), model.DecisionLogRecord{
			Tenant: tenant, JobID: fmt.Sprintf("approval-%d", i), RuleID: tenant,
			Timestamp: since + 1, Verdict: model.SafetyRequireApproval,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	read := func(tenant string) approvalAnalyticsResponse {
		t.Helper()
		path := fmt.Sprintf("/api/v1/governance/approvals/analytics?since=%d&until=%d&group_by=rule", since, since+1000)
		rec := httptest.NewRecorder()
		s.handleApprovalAnalytics(rec, tenantCacheRequest(httptest.NewRequest(http.MethodGet, path, nil), tenant, "admin"))
		var body approvalAnalyticsResponse
		tenantCacheBody(t, rec, &body)
		return body
	}
	for _, tenant := range []string{"cache-a", "cache-b", "cache-a"} {
		want := 1
		if tenant == "cache-b" {
			want = 2
		}
		first := read(tenant)
		before := testutil.ToFloat64(approvalAnalyticsCacheHits.WithLabelValues("hit"))
		cached := read(tenant)
		if testutil.ToFloat64(approvalAnalyticsCacheHits.WithLabelValues("hit"))-before != 1 {
			t.Fatal("repeat did not hit analytics cache")
		}
		for _, got := range []approvalAnalyticsResponse{first, cached} {
			if got.Summary.Total != want || len(got.Groups) != 1 || got.Groups[0].Key != tenant || got.Groups[0].Total != want {
				t.Fatalf("%s: %+v", tenant, got)
			}
		}
	}
}

func TestTenantIsolationShadowSummaryCache(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	resetShadowSummaryCache()
	t.Cleanup(resetShadowSummaryCache)
	base := time.UnixMilli(1_700_000_000_000).UTC()
	for i, tenant := range []string{"cache-a", "cache-b", "cache-b"} {
		seedShadowEventViaGatewayClient(t, s, tenant, shadowEvent(int64(i+1), base, tenant, "bundle", tenant, "escalated", "allow", "deny"))
	}
	read := func(tenant string) ShadowResultsSummary {
		t.Helper()
		req := newShadowResultsRequest("bundle", base.UnixMilli()-1, base.UnixMilli()+1000, "summary")
		rec := httptest.NewRecorder()
		s.handleShadowResultsSummary(rec, tenantCacheRequest(req, tenant, "admin"))
		var body ShadowResultsSummary
		tenantCacheBody(t, rec, &body)
		return body
	}
	for _, tenant := range []string{"cache-a", "cache-b"} {
		want := int64(1)
		if tenant == "cache-b" {
			want = 2
		}
		first := read(tenant)
		if err := s.redisClient().Del(context.Background(), shadowStreamKey(tenant)).Err(); err != nil {
			t.Fatal(err)
		}
		cached := read(tenant)
		for _, got := range []ShadowResultsSummary{first, cached} {
			if got.TotalEvaluated != want || got.EscalatedCount != want || got.ShadowBundleID != tenant {
				t.Fatalf("%s: %+v", tenant, got)
			}
		}
	}
	if got := read("cache-a"); got.TotalEvaluated != 1 || got.ShadowBundleID != "cache-a" {
		t.Fatalf("A's cached summary changed after B: %+v", got)
	}
}

// Only the A walk is held. B must finish before it is released, so a
// tenant-blind flight key fails without relying on scheduling sleeps.
func tenantAuditFlight(t *testing.T, s *server) (<-chan struct{}, func(), *atomic.Int64) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	calls := &atomic.Int64{}
	withAuditVerifySeam(t, func(ctx context.Context, client redis.UniversalClient, key string, opts audit.VerifyOptions) (*audit.VerifyResult, error) {
		calls.Add(1)
		if key == audit.NewChainer(s.redisClient(), "").StreamKey("cache-a") {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return audit.VerifyChain(ctx, client, key, opts)
	})
	t.Cleanup(unblock)
	return started, unblock, calls
}

func awaitTenantAuditResponse(t *testing.T, result <-chan *httptest.ResponseRecorder, want int) {
	t.Helper()
	select {
	case rec := <-result:
		var body audit.VerifyResult
		tenantCacheBody(t, rec, &body)
		if body.TotalEvents != want || body.VerifiedEvents != want || body.Status != audit.VerifyStatusOK {
			t.Fatalf("wrong tenant's verification: %+v", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tenant verification blocked on another tenant's flight")
	}
}

func TestTenantIsolationAuditVerifyCoalescing(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	seedChain(t, s, "cache-a", 1)
	seedChain(t, s, "cache-b", 2)
	started, release, calls := tenantAuditFlight(t, s)
	var wg sync.WaitGroup
	t.Cleanup(func() { release(); wg.Wait() })
	start := func(tenant string) <-chan *httptest.ResponseRecorder {
		result := make(chan *httptest.ResponseRecorder, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			s.handleAuditVerify(rec, auditVerifyTenantReq(tenant, "/api/v1/audit/verify?tenant="+tenant))
			result <- rec
		}()
		return result
	}
	a := start("cache-a")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("A did not enter verification")
	}
	// A witness joins the actual handler-owned flight. It must share A's
	// result, not execute this fallback; this proves the warm flight exists.
	key := auditVerifySingleflightKey("cache-a", audit.VerifyOptions{RetentionBoundarySeq: 1})
	witness := s.auditVerifyCoalesce.DoChan(key, func() (any, error) {
		return nil, fmt.Errorf("A flight was not shared")
	})
	awaitTenantAuditResponse(t, start("cache-b"), 2)
	release()
	awaitTenantAuditResponse(t, a, 1)
	shared := <-witness
	if shared.Err != nil || !shared.Shared || calls.Load() != 2 {
		t.Fatalf("flight not shared within A or not isolated from B: %+v calls=%d", shared, calls.Load())
	}
}
