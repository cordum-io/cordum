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

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/licensing"
	wf "github.com/cordum/cordum/core/workflow"
)

func tenantCounterTotal(t *testing.T, s *server, pattern string) int64 {
	t.Helper()
	keys, err := s.redisClient().Keys(context.Background(), pattern).Result()
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, key := range keys {
		value, err := s.redisClient().Get(context.Background(), key).Int64()
		if err != nil {
			t.Fatal(err)
		}
		total += value
	}
	return total
}

func checkTenantRateLimit(t *testing.T, limiter rateLimiter, firstStatus int) {
	t.Helper()
	var reached []string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.Header.Get("X-Tenant-ID"))
		w.WriteHeader(http.StatusNoContent)
	})
	handler := rateLimitMiddleware(governanceAuth{}, limiter, nil, next)
	for i, tenant := range []string{"counter-a", "counter-a", "counter-b"} {
		want := http.StatusTooManyRequests
		switch i {
		case 0:
			want = firstStatus
		case 2:
			want = http.StatusNoContent
		}
		req := tenantCacheRequest(httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil), tenant, "admin")
		req.RemoteAddr = "192.0.2.1:1234" // same IP: only the authenticated tenant differs
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d: status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	wantReached := []string{"counter-b"}
	if firstStatus == http.StatusNoContent {
		wantReached = []string{"counter-a", "counter-b"}
	}
	if !reflect.DeepEqual(reached, wantReached) {
		t.Fatalf("throttled request reached downstream or B was blocked: %v", reached)
	}
}

func TestTenantIsolationMemoryRateLimit(t *testing.T) {
	limiter := newKeyedRateLimiter(1, 1)
	limiter.rps = 0 // no refill while this test exhausts each tenant's bucket
	checkTenantRateLimit(t, limiter, http.StatusNoContent)
	a, b := limiter.buckets["tenant:counter-a"], limiter.buckets["tenant:counter-b"]
	if len(limiter.buckets) != 2 || a == nil || b == nil || a.tokens != 0 || b.tokens != 0 {
		t.Fatalf("tenant budgets not independently consumed: %+v", limiter.buckets)
	}
}

func TestTenantIsolationRedisRateLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Saturate A across adjacent second windows so crossing a wall-clock
	// boundary cannot accidentally replenish its budget during the requests.
	const windows = 60
	now := time.Now().Unix()
	for i := range windows {
		key := fmt.Sprintf("cordum:rl:tenant:counter-a:%d", now+int64(i))
		if err := s.redisClient().Set(context.Background(), key, 1, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	checkTenantRateLimit(t, newRedisRateLimiter(s.redisClient(), 1, 1), http.StatusTooManyRequests)
	if got := tenantCounterTotal(t, s, "cordum:rl:tenant:counter-a:*"); got != windows+2 {
		t.Fatalf("A's counter was not incremented twice: %d", got)
	}
	if got := tenantCounterTotal(t, s, "cordum:rl:tenant:counter-b:*"); got != 1 {
		t.Fatalf("B's count includes A or was not consumed: %d", got)
	}
}

func TestTenantIsolationRedisIPRateLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	const windows = 60
	now := time.Now().Unix()
	for i := range windows {
		key := fmt.Sprintf("cordum:rl:ip:192.0.2.1:%d", now+int64(i))
		if err := s.redisClient().Set(context.Background(), key, 1, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	var reached []string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.RemoteAddr)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := rateLimitMiddleware(governanceAuth{}, nil, newRedisRateLimiter(s.redisClient(), 1, 1), next)
	for i, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		// Public health reads use IP budgets, even with an authenticated tenant.
		req := tenantCacheRequest(httptest.NewRequest(http.MethodGet, "/health", nil), "counter-a", "admin")
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		want := []int{http.StatusTooManyRequests, http.StatusNoContent}[i]
		if rec.Code != want {
			t.Fatalf("%s: status=%d body=%s", ip, rec.Code, rec.Body.String())
		}
	}
	if !reflect.DeepEqual(reached, []string{"192.0.2.2:1234"}) {
		t.Fatalf("wrong IP admitted downstream: %v", reached)
	}
	if a, b := tenantCounterTotal(t, s, "cordum:rl:ip:192.0.2.1:*"), tenantCounterTotal(t, s, "cordum:rl:ip:192.0.2.2:*"); a != windows+1 || b != 1 {
		t.Fatalf("IP budgets not independently incremented: A=%d B=%d", a, b)
	}
}

func issueTenantDelegation(t *testing.T, s *server, tenant string, want int) delegateTokenResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/shared-agent/delegate",
		strings.NewReader(`{"target_agent_id":"target","allowed_actions":["read"],"allowed_topics":["job.test"]}`))
	req.SetPathValue("id", "shared-agent")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleDelegateAgent(rec, tenantCacheRequest(req, tenant, "admin"))
	if rec.Code != want {
		t.Fatalf("%s: status=%d body=%s", tenant, rec.Code, rec.Body.String())
	}
	var result delegateTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if want == http.StatusTooManyRequests {
		if result.Token != "" || result.JTI != "" {
			t.Fatal("quota denial released a delegation token")
		}
		return result
	}
	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatal(err)
	}
	claims, err := service.VerifyDelegationToken(context.Background(), result.Token, "target")
	if err != nil || claims.Tenant != tenant || claims.Subject != "shared-agent" {
		t.Fatalf("wrong tenant's issued token: claims=%+v err=%v", claims, err)
	}
	return result
}

func TestTenantIsolationDelegationIssueCounter(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	setDelegationKeys(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(e *licensing.Entitlements) { e.AgentIdentity = true })
	for _, tenant := range []string{"counter-a", "counter-b"} {
		// Agent IDs are globally stored. Rebind the same fixture IDs for B,
		// retaining A's quota keys to isolate the counter's tenant dimension.
		for _, id := range []string{"shared-agent", "target"} {
			createDelegationAgent(t, s, tenant, id, []string{"read"}, []string{"job.test"})
		}
		issueTenantDelegation(t, s, tenant, http.StatusCreated)
		if got := tenantCounterTotal(t, s, "delegation:issue:"+tenant+":shared-agent:*"); got != 1 {
			t.Fatalf("%s first issue count=%d", tenant, got)
		}
		if tenant == "counter-a" {
			now := time.Now().UTC()
			for _, at := range []time.Time{now, now.Add(time.Minute)} {
				key := "delegation:issue:counter-a:shared-agent:" + at.Format("200601021504")
				if err := s.redisClient().Set(context.Background(), key, delegationIssueLimitPerMinute, 2*time.Minute).Err(); err != nil {
					t.Fatal(err)
				}
			}
			issueTenantDelegation(t, s, tenant, http.StatusTooManyRequests)
		}
	}
	if got := tenantCounterTotal(t, s, "delegation:issue:counter-a:shared-agent:*"); got != 2*delegationIssueLimitPerMinute+1 {
		t.Fatalf("A's quota was not incremented, or B consumed it: %d", got)
	}
}

func tenantWorkflowAdmissionServer(t *testing.T) *server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	var doc configsvc.Document
	if err := json.Unmarshal([]byte(`{"scope":"system","scope_id":"default","data":{"limits":{"max_concurrent_runs":1}}}`), &doc); err != nil {
		t.Fatal(err)
	}
	if err := s.configSvc.Set(context.Background(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"counter-a", "counter-b"} {
		def := &wf.Workflow{ID: "workflow-" + tenant, OrgID: tenant, Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		}}
		if err := s.workflowStore.SaveWorkflow(context.Background(), def); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func startTenantWorkflow(t *testing.T, s *server, tenant string, want int) {
	t.Helper()
	id := "workflow-" + tenant
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+id+"/runs", strings.NewReader(`{}`))
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.handleStartRun(rec, tenantCacheRequest(req, tenant, "admin"))
	if rec.Code != want {
		t.Fatalf("%s: status=%d body=%s", tenant, rec.Code, rec.Body.String())
	}
	var body struct {
		RunID          string `json:"run_id"`
		Current, Limit int
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if want == http.StatusOK {
		run, err := s.workflowStore.GetRun(context.Background(), body.RunID)
		if err != nil || run == nil || run.OrgID != tenant || run.WorkflowID != id {
			t.Fatalf("wrong tenant's admitted run: %+v err=%v", run, err)
		}
	} else if body.RunID != "" || body.Current != 1 || body.Limit != 1 {
		t.Fatalf("wrong scoped denial: %s", rec.Body.String())
	}
}

func TestTenantIsolationWorkflowRunAdmission(t *testing.T) {
	s := tenantWorkflowAdmissionServer(t)
	startTenantWorkflow(t, s, "counter-a", http.StatusOK)
	startTenantWorkflow(t, s, "counter-a", http.StatusTooManyRequests)
	ctx := context.Background()
	releaseA, err := s.acquireWorkflowAdmissionLock(ctx, "counter-a")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	startTenantWorkflow(t, s, "counter-b", http.StatusOK)
	for _, tenant := range []string{"counter-a", "counter-b"} {
		count, err := s.workflowStore.CountActiveRuns(ctx, tenant)
		if err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", tenant, count, err)
		}
		runs, err := s.workflowStore.ListRunsByWorkflow(ctx, "workflow-"+tenant, 10)
		if err != nil || len(runs) != 1 || runs[0].OrgID != tenant {
			t.Fatalf("%s: foreign or denied run persisted: %+v err=%v", tenant, runs, err)
		}
	}
	if n, err := s.redisClient().Exists(ctx, workflowAdmissionLockKey("counter-a")).Result(); err != nil || n != 1 {
		t.Fatalf("B's admission consumed A's lock: exists=%d err=%v", n, err)
	}
}
