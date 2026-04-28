package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"
)

// seedChain appends n events through a real Chainer to the gateway's
// shared Redis client. Returns the events so tests can later mutate or
// delete entries to simulate tampering.
func seedChain(t *testing.T, s *server, tenant string, n int) []audit.SIEMEvent {
	t.Helper()
	chainer := audit.NewChainer(s.redisClient(), "")
	events := make([]audit.SIEMEvent, 0, n)
	for i := 0; i < n; i++ {
		ev := audit.SIEMEvent{
			EventType: audit.EventSafetyDecision,
			Severity:  audit.SeverityInfo,
			TenantID:  tenant,
			Action:    "seed",
			JobID:     "job-" + strconv.Itoa(i),
		}
		if err := chainer.Append(context.Background(), &ev); err != nil {
			t.Fatalf("seed append[%d]: %v", i, err)
		}
		events = append(events, ev)
	}
	return events
}

func withAuditVerifySeam(t *testing.T, fn func(context.Context, redis.UniversalClient, string, audit.VerifyOptions) (*audit.VerifyResult, error)) {
	t.Helper()
	orig := auditVerifyChainFn
	auditVerifyChainFn = fn
	t.Cleanup(func() { auditVerifyChainFn = orig })
}

func concurrentAuditVerifyBodies(t *testing.T, s *server, paths []string) []string {
	t.Helper()
	reqs := make([]*http.Request, len(paths))
	for i, path := range paths {
		reqs[i] = adminCtx(httptest.NewRequest(http.MethodGet, path, nil))
	}
	return concurrentAuditVerifyRequests(t, s, reqs)
}

func concurrentAuditVerifyRequests(t *testing.T, s *server, reqs []*http.Request) []string {
	t.Helper()
	startCh := make(chan struct{})
	var wg sync.WaitGroup
	bodies := make([]string, len(reqs))
	for i, req := range reqs {
		wg.Add(1)
		go func(i int, req *http.Request) {
			defer wg.Done()
			<-startCh
			rec := httptest.NewRecorder()
			s.handleAuditVerify(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("request %d status=%d want 200 body=%s", i, rec.Code, rec.Body.String())
				return
			}
			bodies[i] = rec.Body.String()
		}(i, req)
	}
	close(startCh)
	wg.Wait()
	return bodies
}

func TestHandleAuditVerify_CoalescesConcurrentRequests(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 200)

	var calls int64
	withAuditVerifySeam(t, func(ctx context.Context, client redis.UniversalClient, streamKey string, opts audit.VerifyOptions) (*audit.VerifyResult, error) {
		atomic.AddInt64(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return audit.VerifyChain(ctx, client, streamKey, opts)
	})

	paths := make([]string, 20)
	for i := range paths {
		paths[i] = "/api/v1/audit/verify?tenant=default"
	}
	bodies := concurrentAuditVerifyBodies(t, s, paths)
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("underlying VerifyChain calls = %d, want 1 for 20 identical concurrent requests", got)
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("body[%d] differs from body[0]\nbody[0]=%s\nbody[%d]=%s", i, bodies[0], i, bodies[i])
		}
	}
}

func TestHandleAuditVerify_DifferentTenantsDoNotCoalesce(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)
	seedChain(t, s, "other", 5)

	var calls int64
	withAuditVerifySeam(t, func(ctx context.Context, client redis.UniversalClient, streamKey string, opts audit.VerifyOptions) (*audit.VerifyResult, error) {
		atomic.AddInt64(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return audit.VerifyChain(ctx, client, streamKey, opts)
	})

	concurrentAuditVerifyRequests(t, s, []*http.Request{
		adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil)),
		adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil)),
		auditVerifyTenantReq("other", "/api/v1/audit/verify?tenant=other"),
		auditVerifyTenantReq("other", "/api/v1/audit/verify?tenant=other"),
	})
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("underlying VerifyChain calls = %d, want 2 for two distinct tenants", got)
	}
}

func TestHandleAuditVerify_CoalescerScopedPerServer(t *testing.T) {
	s1, _, _ := newTestGateway(t)
	s2, _, _ := newTestGateway(t)
	seedChain(t, s1, "default", 3)
	seedChain(t, s2, "default", 7)

	var calls int64
	withAuditVerifySeam(t, func(ctx context.Context, client redis.UniversalClient, streamKey string, opts audit.VerifyOptions) (*audit.VerifyResult, error) {
		atomic.AddInt64(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return audit.VerifyChain(ctx, client, streamKey, opts)
	})

	startCh := make(chan struct{})
	var wg sync.WaitGroup
	bodies := make([]string, 2)
	for i, srv := range []*server{s1, s2} {
		wg.Add(1)
		go func(i int, srv *server) {
			defer wg.Done()
			<-startCh
			req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
			rec := httptest.NewRecorder()
			srv.handleAuditVerify(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("server %d status=%d want 200 body=%s", i, rec.Code, rec.Body.String())
				return
			}
			bodies[i] = rec.Body.String()
		}(i, srv)
	}
	close(startCh)
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("underlying VerifyChain calls = %d, want 2 for identical keys on distinct server backends", got)
	}
	for i, want := range []int{3, 7} {
		var res audit.VerifyResult
		if err := json.Unmarshal([]byte(bodies[i]), &res); err != nil {
			t.Fatalf("decode server %d body: %v body=%s", i, err, bodies[i])
		}
		if res.TotalEvents != want {
			t.Fatalf("server %d TotalEvents=%d, want %d", i, res.TotalEvents, want)
		}
	}
}

func TestHandleAuditVerify_ImplicitAndExplicitDefaultLimitCoalesce(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)

	var calls int64
	withAuditVerifySeam(t, func(ctx context.Context, client redis.UniversalClient, streamKey string, opts audit.VerifyOptions) (*audit.VerifyResult, error) {
		atomic.AddInt64(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return audit.VerifyChain(ctx, client, streamKey, opts)
	})

	explicitDefaultLimit := strconv.FormatInt(audit.DefaultVerifyLimit, 10)
	paths := make([]string, 20)
	for i := range paths {
		paths[i] = "/api/v1/audit/verify?tenant=default&since=1000"
		if i%2 == 1 {
			paths[i] += "&limit=" + explicitDefaultLimit
		}
	}

	concurrentAuditVerifyBodies(t, s, paths)
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("underlying VerifyChain calls = %d, want 1 for implicit and explicit default limit", got)
	}
}

func TestAuditVerifySingleflightKeyIncludesHMACFingerprint(t *testing.T) {
	base := audit.VerifyOptions{
		SinceMs:              100,
		UntilMs:              200,
		Limit:                audit.DefaultVerifyLimit,
		RetentionBoundarySeq: 7,
	}
	noHMACKey := auditVerifySingleflightKey("default", base)

	base.HMACKey = []byte("hmac-secret-a")
	hmacKeyA := auditVerifySingleflightKey("default", base)
	if hmacKeyA == noHMACKey {
		t.Fatal("singleflight key did not change when HMAC verification was enabled")
	}
	if strings.Contains(hmacKeyA, "hmac-secret-a") {
		t.Fatal("singleflight key exposes raw HMAC key material")
	}

	copied := base
	copied.HMACKey = []byte("hmac-secret-a")
	if got := auditVerifySingleflightKey("default", copied); got != hmacKeyA {
		t.Fatalf("singleflight key for identical HMAC key = %q, want %q", got, hmacKeyA)
	}

	base.HMACKey = []byte("hmac-secret-b")
	if got := auditVerifySingleflightKey("default", base); got == hmacKeyA {
		t.Fatal("singleflight key coalesced different HMAC keys")
	}
}

func TestHandleAuditVerify_DifferentSinceDoesNotCoalesce(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)

	var calls int64
	withAuditVerifySeam(t, func(ctx context.Context, client redis.UniversalClient, streamKey string, opts audit.VerifyOptions) (*audit.VerifyResult, error) {
		atomic.AddInt64(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return audit.VerifyChain(ctx, client, streamKey, opts)
	})

	concurrentAuditVerifyBodies(t, s, []string{
		"/api/v1/audit/verify?tenant=default&since=1000",
		"/api/v1/audit/verify?tenant=default&since=1000",
		"/api/v1/audit/verify?tenant=default&since=2000",
		"/api/v1/audit/verify?tenant=default&since=2000",
	})
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("underlying VerifyChain calls = %d, want 2 for two distinct since cursors", got)
	}
}

func TestHandleAuditVerify_LeaderErrorPropagatesToWaiters(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)

	sentinel := errors.New("sentinel verify failure")
	var calls int64
	beforeCoalesced := testutil.ToFloat64(auditVerifyCoalescedTotal)
	withAuditVerifySeam(t, func(context.Context, redis.UniversalClient, string, audit.VerifyOptions) (*audit.VerifyResult, error) {
		atomic.AddInt64(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return nil, sentinel
	})

	startCh := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 5)
	bodies := make([]string, 5)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-startCh
			req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
			rec := httptest.NewRecorder()
			s.handleAuditVerify(rec, req)
			codes[i] = rec.Code
			bodies[i] = rec.Body.String()
		}(i)
	}
	close(startCh)
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("underlying VerifyChain calls = %d, want 1 shared failed walk", got)
	}
	for i, code := range codes {
		if code != http.StatusInternalServerError {
			t.Fatalf("request %d status=%d want 500 body=%s", i, code, bodies[i])
		}
	}
	if got := testutil.ToFloat64(auditVerifyCoalescedTotal) - beforeCoalesced; got != float64(len(codes)-1) {
		t.Fatalf("coalesced counter delta = %v, want %d for waiters on shared error", got, len(codes)-1)
	}
}

func TestHandleAuditVerify_CancelledFollowerStopsWaiting(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)

	enteredVerify := make(chan struct{})
	releaseVerify := make(chan struct{})
	var enteredOnce sync.Once
	withAuditVerifySeam(t, func(ctx context.Context, client redis.UniversalClient, streamKey string, opts audit.VerifyOptions) (*audit.VerifyResult, error) {
		enteredOnce.Do(func() { close(enteredVerify) })
		select {
		case <-releaseVerify:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return audit.VerifyChain(ctx, client, streamKey, opts)
	})

	leaderDone := make(chan int, 1)
	go func() {
		req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
		rec := httptest.NewRecorder()
		s.handleAuditVerify(rec, req)
		leaderDone <- rec.Code
	}()

	select {
	case <-enteredVerify:
	case <-time.After(time.Second):
		t.Fatal("leader did not enter verify seam")
	}

	followerCtx, cancelFollower := context.WithCancel(context.Background())
	followerReq := httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil).WithContext(followerCtx)
	followerReq = adminCtx(followerReq)
	followerDone := make(chan struct{})
	go func() {
		rec := httptest.NewRecorder()
		s.handleAuditVerify(rec, followerReq)
		close(followerDone)
	}()

	time.Sleep(20 * time.Millisecond)
	cancelFollower()
	select {
	case <-followerDone:
	case <-time.After(200 * time.Millisecond):
		close(releaseVerify)
		t.Fatal("cancelled follower remained blocked on shared verify result")
	}

	close(releaseVerify)
	select {
	case code := <-leaderDone:
		if code != http.StatusOK {
			t.Fatalf("leader status=%d want 200", code)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not finish after releasing verify seam")
	}
}

func TestHandleAuditVerify_ShutdownCancelsLeaderWalk(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})
	seedChain(t, s, "default", 5)

	enteredVerify := make(chan struct{})
	var enteredOnce sync.Once
	withAuditVerifySeam(t, func(ctx context.Context, _ redis.UniversalClient, _ string, _ audit.VerifyOptions) (*audit.VerifyResult, error) {
		enteredOnce.Do(func() { close(enteredVerify) })
		<-ctx.Done()
		return nil, ctx.Err()
	})

	done := make(chan int, 1)
	go func() {
		req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
		rec := httptest.NewRecorder()
		s.handleAuditVerify(rec, req)
		done <- rec.Code
	}()

	select {
	case <-enteredVerify:
	case <-time.After(time.Second):
		t.Fatal("leader did not enter verify seam")
	}
	close(s.shutdownCh)

	select {
	case code := <-done:
		if code != http.StatusInternalServerError {
			t.Fatalf("status=%d want 500 after shutdown cancels verify walk", code)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("verify walk did not stop promptly after shutdown")
	}
}

func TestHandleAuditVerify_LeaderContextCancelDoesNotPoisonWaiters(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)

	var calls int64
	enteredVerify := make(chan struct{})
	var enteredOnce sync.Once
	withAuditVerifySeam(t, func(ctx context.Context, client redis.UniversalClient, streamKey string, opts audit.VerifyOptions) (*audit.VerifyResult, error) {
		atomic.AddInt64(&calls, 1)
		enteredOnce.Do(func() { close(enteredVerify) })
		time.Sleep(100 * time.Millisecond)
		return audit.VerifyChain(ctx, client, streamKey, opts)
	})

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderReq := httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil).WithContext(leaderCtx)
	leaderReq = adminCtx(leaderReq)

	var wg sync.WaitGroup
	codes := make([]int, 3)

	wg.Add(1)
	go func() {
		defer wg.Done()
		rec := httptest.NewRecorder()
		s.handleAuditVerify(rec, leaderReq)
		codes[0] = rec.Code
	}()

	select {
	case <-enteredVerify:
	case <-time.After(time.Second):
		t.Fatal("leader did not enter verify seam")
	}
	cancelLeader()

	for i := 1; i < len(codes); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
			rec := httptest.NewRecorder()
			s.handleAuditVerify(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("underlying VerifyChain calls = %d, want 1 detached shared walk", got)
	}
	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("request %d status=%d want 200", i, code)
		}
	}
}

func TestHandleAuditVerify_MetricsExposed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)

	beforeDurationCount := auditVerifyDurationSampleCount(t, string(audit.VerifyStatusOK))
	beforeEvents := testutil.ToFloat64(auditVerifyEventsTotal.WithLabelValues(string(audit.VerifyStatusOK)))
	beforeCoalesced := testutil.ToFloat64(auditVerifyCoalescedTotal)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if got := auditVerifyDurationSampleCount(t, string(audit.VerifyStatusOK)); got < beforeDurationCount+1 {
		t.Fatalf("duration histogram sample count = %d, want at least %d", got, beforeDurationCount+1)
	}
	if got := testutil.ToFloat64(auditVerifyEventsTotal.WithLabelValues(string(audit.VerifyStatusOK))); got-beforeEvents != 5 {
		t.Fatalf("events_total delta = %v, want 5", got-beforeEvents)
	}
	if got := testutil.ToFloat64(auditVerifyInflight); got != 0 {
		t.Fatalf("inflight gauge = %v, want 0 after request", got)
	}
	if got := testutil.CollectAndCount(auditVerifyCoalescedTotal); got == 0 {
		t.Fatalf("coalesced counter metric is not registered")
	}
	if got := testutil.ToFloat64(auditVerifyCoalescedTotal); got < beforeCoalesced {
		t.Fatalf("coalesced counter regressed from %v to %v", beforeCoalesced, got)
	}
}

func auditVerifyTenantReq(tenant, path string) *http.Request {
	return withAuth(httptest.NewRequest(http.MethodGet, path, nil), &auth.AuthContext{
		Role:        "admin",
		Tenant:      tenant,
		PrincipalID: "admin@example.com",
	})
}

func auditVerifyDurationSampleCount(t *testing.T, status string) uint64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 16)
	auditVerifyDuration.Collect(ch)
	close(ch)

	var count uint64
	for metric := range ch {
		var dtoMetric dto.Metric
		if err := metric.Write(&dtoMetric); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		hasStatus := false
		for _, label := range dtoMetric.GetLabel() {
			if label.GetName() == "status" && label.GetValue() == status {
				hasStatus = true
				break
			}
		}
		if hasStatus && dtoMetric.GetHistogram() != nil {
			count += dtoMetric.GetHistogram().GetSampleCount()
		}
	}
	return count
}

func TestHandleAuditVerify_IntactChain(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var res audit.VerifyResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Status != audit.VerifyStatusOK {
		t.Errorf("Status = %q, want ok", res.Status)
	}
	if res.TotalEvents != 5 || res.VerifiedEvents != 5 {
		t.Errorf("TotalEvents=%d VerifiedEvents=%d, want 5/5", res.TotalEvents, res.VerifiedEvents)
	}
	if len(res.Gaps) != 0 {
		t.Errorf("Gaps = %+v, want empty", res.Gaps)
	}
	if res.FirstSeq != 1 || res.LastSeq != 5 {
		t.Errorf("FirstSeq=%d LastSeq=%d", res.FirstSeq, res.LastSeq)
	}
}

// TestHandleAuditVerify_DetectsModifiedEvent flips a byte in an event's
// payload and asserts the handler reports status=compromised with a
// hash_mismatch gap at the tampered seq.
//
// Miniredis refuses XADD at an existing stream ID, so we delete the
// entry and re-add with the same seq at a fresh (later) ID. The walker
// will see seq=3 at the tail, which triggers both hash_mismatch (the
// event bytes no longer hash to the stored EventHash) and a gap at
// seq=3 in the middle; either is a sufficient compromised signal.
func TestHandleAuditVerify_DetectsModifiedEvent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)

	ctx := context.Background()
	streamKey := audit.NewChainer(s.redisClient(), "").StreamKey("default")
	entries, err := s.redisClient().XRange(ctx, streamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	target := entries[2]
	var ev audit.SIEMEvent
	payload, _ := target.Values["event"].(string)
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ev.Action = "MUTATED"
	mutated, _ := json.Marshal(ev)

	if err := s.redisClient().XDel(ctx, streamKey, target.ID).Err(); err != nil {
		t.Fatalf("xdel: %v", err)
	}
	// XADD with "*" picks a stream ID greater than any existing — lets
	// the re-insert succeed despite miniredis's monotonic-ID rule.
	if err := s.redisClient().XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "*",
		Values: map[string]any{
			"seq":   strconv.FormatInt(ev.Seq, 10),
			"event": string(mutated),
		},
	}).Err(); err != nil {
		t.Fatalf("xadd: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var res audit.VerifyResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Status != audit.VerifyStatusCompromised {
		t.Fatalf("Status = %q, want compromised: %+v", res.Status, res)
	}
	if len(res.Gaps) == 0 {
		t.Fatalf("expected at least one gap, got %+v", res)
	}
}

// TestHandleAuditVerify_DetectsDeletedEvent removes a middle event and
// asserts the gap is reported.
func TestHandleAuditVerify_DetectsDeletedEvent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 5)

	ctx := context.Background()
	streamKey := audit.NewChainer(s.redisClient(), "").StreamKey("default")
	entries, err := s.redisClient().XRange(ctx, streamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	// Delete entry #3 (seq=3).
	if err := s.redisClient().XDel(ctx, streamKey, entries[2].ID).Err(); err != nil {
		t.Fatalf("xdel: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)

	var res audit.VerifyResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Status != audit.VerifyStatusCompromised {
		t.Fatalf("Status = %q, want compromised", res.Status)
	}
	// Deleting seq=3 will also break the PrevHash chain for seq=4.
	// Expect at least a missing gap at seq=3.
	sawMissing := false
	for _, g := range res.Gaps {
		if g.AtSeq == 3 && g.Type == audit.GapTypeMissing {
			sawMissing = true
		}
	}
	if !sawMissing {
		t.Errorf("expected missing gap at seq=3, got %+v", res.Gaps)
	}
}

// TestHandleAuditVerify_EmptyChain — a correctly-configured tenant
// with no activity should return status=ok with zero totals, not an
// error. The handler's fail-loud guard only kicks in when BOTH the
// chainer is absent AND no events have ever been chained — a
// misconfigured deploy. Installing a chainer takes us off that path.
func TestHandleAuditVerify_EmptyChain(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auditChainer = audit.NewChainer(s.redisClient(), "")
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var res audit.VerifyResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Status != audit.VerifyStatusOK || res.TotalEvents != 0 {
		t.Errorf("unexpected result: %+v", res)
	}
}

// TestHandleAuditVerify_NoChainerAndNoEventsIs503 pins the fail-loud
// guard introduced by the verify-handler QA note: when the chainer is
// not installed AND the tenant's stream is empty, the endpoint must
// return 503 so a misconfigured deploy cannot quietly produce a
// false-green "ok, 0 events" result that sails through a compliance
// audit.
func TestHandleAuditVerify_NoChainerAndNoEventsIs503(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auditChainer = nil
	// auditChainer left nil on purpose; no seeded events either.
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestHandleAuditVerify_Requires503WhenRedisMissing verifies the
// requireStoreAndRole guard returns 503 when the Redis client is
// unavailable. Role enforcement itself is exercised in
// helpers_test.go / handlers_auth_test.go — duplicating it here would
// require a full auth provider setup.
func TestHandleAuditVerify_Requires503WhenRedisMissing(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.jobStore = nil // redisClient() returns nil

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestHandleAuditVerify_BadLimit enforces the limit query validation.
func TestHandleAuditVerify_BadLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	cases := []string{"-1", "0", "abc", "999999999"}
	for _, v := range cases {
		req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default&limit="+v, nil))
		rec := httptest.NewRecorder()
		s.handleAuditVerify(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("limit=%q: status=%d, want 400", v, rec.Code)
		}
	}
}

// TestHandleAuditVerify_SinceUntilRangeValidation covers since > until
// and since/until spread > 30 days.
func TestHandleAuditVerify_SinceUntilRangeValidation(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// until < since → 400.
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default&since=2000&until=1000", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("since>until: status=%d, want 400", rec.Code)
	}

	// Spread > 30 days. since must be >0 for the spread guard to engage.
	req = adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default&since=1&until=9999999999999", nil))
	rec = httptest.NewRecorder()
	s.handleAuditVerify(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("spread>30d: status=%d, want 400", rec.Code)
	}
}

// TestHandleAuditVerify_RetentionTrimmedPrefix seeds 8 events, manually
// removes seqs 1-3 from the stream (simulating retention expiry), and
// asserts the result reports retention_boundary_seq=4 with the gap at
// 1-3 classified as retention_trimmed (not tampering).
func TestHandleAuditVerify_RetentionTrimmedPrefix(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 8)

	ctx := context.Background()
	streamKey := audit.NewChainer(s.redisClient(), "").StreamKey("default")
	entries, err := s.redisClient().XRange(ctx, streamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	// Delete stream entries backing seqs 1..3.
	for i := 0; i < 3; i++ {
		if err := s.redisClient().XDel(ctx, streamKey, entries[i].ID).Err(); err != nil {
			t.Fatalf("xdel[%d]: %v", i, err)
		}
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var res audit.VerifyResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.RetentionBoundarySeq != 4 {
		t.Errorf("RetentionBoundarySeq = %d, want 4", res.RetentionBoundarySeq)
	}
	if res.FirstSeq != 4 || res.LastSeq != 8 {
		t.Errorf("FirstSeq=%d LastSeq=%d, want 4/8", res.FirstSeq, res.LastSeq)
	}
	// Prefix gaps at 1, 2, 3 must be classified retention_trimmed.
	trimmed := make(map[int64]bool)
	for _, g := range res.Gaps {
		if g.Type == audit.GapTypeRetentionTrimmed {
			trimmed[g.AtSeq] = true
		}
		if g.Type == audit.GapTypeMissing {
			t.Errorf("unexpected missing gap at seq=%d; prefix should be retention_trimmed", g.AtSeq)
		}
	}
	for _, want := range []int64{1, 2, 3} {
		if !trimmed[want] {
			t.Errorf("expected retention_trimmed gap at seq=%d, got %+v", want, res.Gaps)
		}
	}
	// Trimmed prefix alone must not flip status to compromised.
	if res.Status == audit.VerifyStatusCompromised {
		t.Errorf("retention-only trimming must not be compromised: %+v", res)
	}
}

// TestHandleAuditVerify_WithinWalkGapIsMissing seeds 8 events, deletes
// a middle entry (seq=5), and asserts the gap is classified missing
// (tampering) because it sits above the retention boundary.
func TestHandleAuditVerify_WithinWalkGapIsMissing(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 8)

	ctx := context.Background()
	streamKey := audit.NewChainer(s.redisClient(), "").StreamKey("default")
	entries, err := s.redisClient().XRange(ctx, streamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	// Delete the entry at seq=5 (index 4).
	if err := s.redisClient().XDel(ctx, streamKey, entries[4].ID).Err(); err != nil {
		t.Fatalf("xdel: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)

	var res audit.VerifyResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Status != audit.VerifyStatusCompromised {
		t.Fatalf("Status = %q, want compromised: %+v", res.Status, res)
	}
	sawMissingAt5 := false
	for _, g := range res.Gaps {
		if g.AtSeq == 5 {
			if g.Type != audit.GapTypeMissing {
				t.Errorf("gap at seq=5 classified %q, want missing", g.Type)
			}
			sawMissingAt5 = true
		}
	}
	if !sawMissingAt5 {
		t.Errorf("expected missing gap at seq=5, got %+v", res.Gaps)
	}
}

// TestHandleAuditVerify_ReportsRetentionWindowHours asserts the handler
// echoes back the CORDUM_AUDIT_RETENTION_HOURS value so dashboards can
// render "your policy is N hours" without round-tripping to config.
func TestHandleAuditVerify_ReportsRetentionWindowHours(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_RETENTION_HOURS", "72")
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 2)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)

	var res audit.VerifyResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.RetentionWindowHours != 72 {
		t.Errorf("RetentionWindowHours = %v, want 72", res.RetentionWindowHours)
	}
}

// TestHandleAuditVerify_NeverReturnsEventBody is a defence-in-depth check:
// the response body must not contain the raw event payload (action,
// reason, job_id, etc.) even if the internal VerifyResult gains fields
// later. The seeded events carry a distinctive "seed" action/"job-N" id
// so the assertion would fail loudly if either leaked.
func TestHandleAuditVerify_NeverReturnsEventBody(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedChain(t, s, "default", 3)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	s.handleAuditVerify(rec, req)

	body := rec.Body.String()
	for _, forbidden := range []string{"\"action\"", "\"reason\"", "\"job_id\"", "\"extra\"", "seed", "job-"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("response leaks %q: %s", forbidden, body)
		}
	}
}

func TestAuditVerifyRouteRegistered(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	seedChain(t, s, "default", 3)

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var res audit.VerifyResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Status != audit.VerifyStatusOK {
		t.Fatalf("status field = %q, want %q", res.Status, audit.VerifyStatusOK)
	}
	if res.TotalEvents != 3 {
		t.Fatalf("total_events = %d, want 3", res.TotalEvents)
	}
	if res.VerifiedEvents != 3 {
		t.Fatalf("verified_events = %d, want 3", res.VerifiedEvents)
	}
}

func TestAuditVerifyRequiresAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	seedChain(t, s, "default", 1)

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil), &auth.AuthContext{
		Role:        "viewer",
		Tenant:      "default",
		PrincipalID: "viewer@example.com",
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}
