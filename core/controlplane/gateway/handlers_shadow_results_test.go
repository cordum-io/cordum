package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/redis/go-redis/v9"
)

// keep imports wired until the rest of the shadow_results_test.go
// coverage is written elsewhere — otherwise goimports keeps tripping
// on net/http, httptest, and strconv.
var _ = []any{http.StatusOK, httptest.NewRecorder, strconv.Itoa, audit.EventSafetyDecision}

// newShadowResultsFixture stands up a miniredis instance and returns a
// client + a helper that appends SIEMEvents to the given stream key.
// Keeps every test below free of boilerplate Redis wiring.
func newShadowResultsFixture(t *testing.T) (redis.UniversalClient, func(streamKey string, ev audit.SIEMEvent), func()) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	client := redis.NewClient(testRedisOptions(srv.Addr()))
	append := func(streamKey string, ev audit.SIEMEvent) {
		t.Helper()
		raw, err := json.Marshal(&ev)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		id := "*"
		if !ev.Timestamp.IsZero() {
			// Pin the stream ID to the event's timestamp so tests can
			// assert ordering and range filtering deterministically.
			id = fmt.Sprintf("%d-%d", ev.Timestamp.UnixMilli(), ev.Seq)
		}
		if _, err := client.XAdd(context.Background(), &redis.XAddArgs{
			Stream: streamKey,
			ID:     id,
			Values: map[string]any{
				"event": string(raw),
				"seq":   fmt.Sprintf("%d", ev.Seq),
			},
		}).Result(); err != nil {
			t.Fatalf("xadd: %v", err)
		}
	}
	cleanup := func() {
		_ = client.Close()
		srv.Close()
	}
	return client, append, cleanup
}

// shadowEvent is a small helper to reduce boilerplate when building
// test SIEMEvents.
func shadowEvent(seq int64, ts time.Time, tenant, bundleID, shadowBundleID, diff, activeVerdict, shadowVerdict string) audit.SIEMEvent {
	return audit.SIEMEvent{
		Timestamp: ts,
		EventType: audit.EventShadowEval,
		Severity:  audit.SeverityInfo,
		TenantID:  tenant,
		JobID:     fmt.Sprintf("job-%d", seq),
		AgentID:   "agent-1",
		Action:    diff,
		Decision:  shadowVerdict,
		Seq:       seq,
		Extra: map[string]string{
			"bundle_id":        bundleID,
			"shadow_bundle_id": shadowBundleID,
			"active_verdict":   activeVerdict,
			"shadow_verdict":   shadowVerdict,
			"diff":             diff,
			"active_rule_id":   "a1",
			"shadow_rule_id":   "s1",
			"latency_ms":       "2",
		},
	}
}

// TestScanShadowEvents_FiltersEventTypeAndBundleID is the canonical
// scanner test: seed mixed events (different types, different bundles,
// different tenants are implicitly isolated by stream key) and assert
// only the target-bundle shadow_eval events are returned.
func TestScanShadowEvents_FiltersEventTypeAndBundleID(t *testing.T) {
	t.Parallel()
	client, seed, cleanup := newShadowResultsFixture(t)
	defer cleanup()

	streamKey := "audit:chain:tenant-a"
	base := time.Now().UTC()

	// 3 target-bundle shadow_eval events
	for i := range 3 {
		seed(streamKey, shadowEvent(int64(i+1), base.Add(time.Duration(i)*time.Millisecond), "tenant-a", "bundle-A", "shadow-A1", "escalated", "allow", "deny"))
	}
	// Different bundle — must be skipped.
	seed(streamKey, shadowEvent(4, base.Add(4*time.Millisecond), "tenant-a", "bundle-B", "shadow-B1", "escalated", "allow", "deny"))
	// Different event type — must be skipped.
	seed(streamKey, audit.SIEMEvent{
		Timestamp: base.Add(5 * time.Millisecond),
		EventType: audit.EventSafetyDecision,
		TenantID:  "tenant-a",
		Seq:       5,
		Extra:     map[string]string{"bundle_id": "bundle-A"},
	})
	// Another target-bundle event after the noise — must be kept.
	seed(streamKey, shadowEvent(6, base.Add(6*time.Millisecond), "tenant-a", "bundle-A", "shadow-A1", "unchanged", "allow", "allow"))

	got, truncated, err := scanShadowEvents(context.Background(), client, streamKey, "bundle-A", 0, 0, 0)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if truncated {
		t.Error("expected not truncated for small fixture")
	}
	if len(got) != 4 {
		t.Fatalf("want 4 events, got %d: %+v", len(got), got)
	}
	for i, ev := range got {
		if ev.EventType != audit.EventShadowEval {
			t.Errorf("event[%d].EventType = %q", i, ev.EventType)
		}
		if ev.Extra["bundle_id"] != "bundle-A" {
			t.Errorf("event[%d].bundle_id = %q", i, ev.Extra["bundle_id"])
		}
	}
}

// TestScanShadowEvents_LimitTruncates pins the truncation contract —
// the scanner stops at `limit` and reports truncatedAtMax so the
// handler can forward the signal.
func TestScanShadowEvents_LimitTruncates(t *testing.T) {
	t.Parallel()
	client, seed, cleanup := newShadowResultsFixture(t)
	defer cleanup()

	streamKey := "audit:chain:tenant-a"
	base := time.Now().UTC()
	for i := range 10 {
		seed(streamKey, shadowEvent(int64(i+1), base.Add(time.Duration(i)*time.Millisecond), "tenant-a", "bundle-A", "shadow-A1", "escalated", "allow", "deny"))
	}

	got, truncated, err := scanShadowEvents(context.Background(), client, streamKey, "bundle-A", 0, 0, 4)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !truncated {
		t.Error("expected truncatedAtMax=true when limit hits")
	}
	if len(got) != 4 {
		t.Errorf("want 4 events, got %d", len(got))
	}
}

// TestScanShadowEvents_RangeClamps confirms the since/until bounds are
// honoured — events outside the range must NOT be returned.
func TestScanShadowEvents_RangeClamps(t *testing.T) {
	t.Parallel()
	client, seed, cleanup := newShadowResultsFixture(t)
	defer cleanup()

	streamKey := "audit:chain:tenant-a"
	base := time.UnixMilli(1_700_000_000_000).UTC()
	// Scatter 10 events one minute apart.
	for i := range 10 {
		ts := base.Add(time.Duration(i) * time.Minute)
		seed(streamKey, shadowEvent(int64(i+1), ts, "tenant-a", "bundle-A", "shadow-A1", "escalated", "allow", "deny"))
	}

	sinceMs := base.Add(3 * time.Minute).UnixMilli()
	untilMs := base.Add(6 * time.Minute).UnixMilli()
	got, _, err := scanShadowEvents(context.Background(), client, streamKey, "bundle-A", sinceMs, untilMs, 0)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 4 { // minutes 3,4,5,6 inclusive
		t.Errorf("want 4 events in range, got %d", len(got))
	}
	for _, ev := range got {
		ms := ev.Timestamp.UnixMilli()
		if ms < sinceMs || ms > untilMs {
			t.Errorf("event ts %d outside [%d, %d]", ms, sinceMs, untilMs)
		}
	}
}

// TestScanShadowEvents_EmptyStreamReturnsEmpty exercises the zero-
// event baseline. The handler layer relies on this returning a nil
// slice + no error (not redis.Nil) for the empty-range UX.
func TestScanShadowEvents_EmptyStreamReturnsEmpty(t *testing.T) {
	t.Parallel()
	client, _, cleanup := newShadowResultsFixture(t)
	defer cleanup()
	got, truncated, err := scanShadowEvents(context.Background(), client, "audit:chain:unknown", "bundle-X", 0, 0, 0)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if truncated {
		t.Error("empty stream must not be truncated")
	}
	if len(got) != 0 {
		t.Errorf("want 0 events, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// Summary handler tests
// ---------------------------------------------------------------------------

// seedShadowEventViaGatewayClient pushes an event into the server's
// shared Redis client. Keeps the seed path aligned with production
// wiring — shadowStreamKey uses audit.NewChainer(..).StreamKey(tenant)
// so the test observes events through the same code path as /summary.
func seedShadowEventViaGatewayClient(t *testing.T, s *server, tenant string, ev audit.SIEMEvent) {
	t.Helper()
	raw, err := json.Marshal(&ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	id := "*"
	if !ev.Timestamp.IsZero() {
		id = fmt.Sprintf("%d-%d", ev.Timestamp.UnixMilli(), ev.Seq)
	}
	if _, err := s.redisClient().XAdd(context.Background(), &redis.XAddArgs{
		Stream: shadowStreamKey(tenant),
		ID:     id,
		Values: map[string]any{"event": string(raw), "seq": fmt.Sprintf("%d", ev.Seq)},
	}).Result(); err != nil {
		t.Fatalf("xadd: %v", err)
	}
}

// newShadowResultsRequest builds a request with the {id} path value
// and from/to query params pre-populated. Mirrors what the real mux
// would do at runtime.
func newShadowResultsRequest(bundleID string, fromMs, toMs int64, subpath string) *http.Request {
	url := fmt.Sprintf("/api/v1/policy/bundles/%s/shadow/results/%s?from=%d&to=%d", bundleID, subpath, fromMs, toMs)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", bundleID)
	return adminCtx(req)
}

// TestHandleShadowResultsSummary_CountsByDiff is the canonical
// reducer test: seed one of each diff class for bundle-A plus two
// noise events on bundle-B, call /summary for bundle-A, assert all
// four counts are 1 and shadow_bundle_id reflects the most recent
// bundle-A event.
func TestHandleShadowResultsSummary_CountsByDiff(t *testing.T) {
	s, _, _ := newTestGateway(t)
	resetShadowSummaryCache() // isolation — previous tests may have populated the cache

	base := time.UnixMilli(1_700_000_000_000).UTC()
	seq := int64(0)
	addA := func(diff, shadowID string, offset time.Duration) {
		seq++
		seedShadowEventViaGatewayClient(t, s, "default", shadowEvent(seq, base.Add(offset), "default", "bundle-A", shadowID, diff, "allow", "deny"))
	}
	addA("escalated", "shadow-A1", 1*time.Millisecond)
	addA("relaxed", "shadow-A1", 2*time.Millisecond)
	addA("approval_differ", "shadow-A1", 3*time.Millisecond)
	// Most-recent wins: bump shadowID on the final A event.
	addA("unchanged", "shadow-A2", 4*time.Millisecond)

	// Noise: different bundle shouldn't leak into the count.
	for range 2 {
		seq++
		seedShadowEventViaGatewayClient(t, s, "default", shadowEvent(seq, base.Add(5*time.Millisecond), "default", "bundle-B", "shadow-B1", "escalated", "allow", "deny"))
	}

	fromMs := base.Add(-time.Second).UnixMilli()
	toMs := base.Add(time.Minute).UnixMilli()
	req := newShadowResultsRequest("bundle-A", fromMs, toMs, "summary")
	rec := httptest.NewRecorder()
	s.handleShadowResultsSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got ShadowResultsSummary
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.BundleID != "bundle-A" {
		t.Errorf("BundleID = %q", got.BundleID)
	}
	if got.TotalEvaluated != 4 {
		t.Errorf("TotalEvaluated = %d, want 4", got.TotalEvaluated)
	}
	if got.EscalatedCount != 1 || got.RelaxedCount != 1 || got.ApprovalDifferCount != 1 || got.UnchangedCount != 1 {
		t.Errorf("counts = %+v, want each=1", got)
	}
	if got.ShadowBundleID != "shadow-A2" {
		t.Errorf("ShadowBundleID = %q, want shadow-A2 (most recent)", got.ShadowBundleID)
	}
	if got.FromMs != fromMs || got.ToMs != toMs {
		t.Errorf("range echoed wrong: from=%d to=%d", got.FromMs, got.ToMs)
	}
	if got.TruncatedAtMax {
		t.Error("4 events should not truncate at max")
	}
}

// TestHandleShadowResultsSummary_MissingRangeReturns400 pins the
// validation UX: missing from/to → 400 with a helpful message rather
// than an empty response or a 500.
func TestHandleShadowResultsSummary_MissingRangeReturns400(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/b/shadow/results/summary", nil)
	req.SetPathValue("id", "b")
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsSummary(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestHandleShadowResultsSummary_Range30DaysRejected keeps the scan
// budget honest — a > 30-day window must 400 at the handler level so
// we never XRANGE past the retention window.
func TestHandleShadowResultsSummary_Range30DaysRejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	fromMs := int64(1_700_000_000_000)
	toMs := fromMs + (31 * 24 * time.Hour).Milliseconds()
	req := newShadowResultsRequest("b", fromMs, toMs, "summary")
	rec := httptest.NewRecorder()
	s.handleShadowResultsSummary(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestHandleShadowResultsSummary_MicroCacheHit confirms the 60 s
// memoisation: a second call with identical (tenant, bundle, from, to)
// reads from the cache. We assert by deleting every stream entry
// between the two calls — if the cache didn't serve, the second call
// would return total=0.
func TestHandleShadowResultsSummary_MicroCacheHit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	resetShadowSummaryCache()

	base := time.UnixMilli(1_700_000_100_000).UTC()
	seedShadowEventViaGatewayClient(t, s, "default", shadowEvent(1, base, "default", "bundle-A", "shadow-A1", "escalated", "allow", "deny"))

	fromMs := base.Add(-time.Second).UnixMilli()
	toMs := base.Add(time.Minute).UnixMilli()

	// First call populates the cache.
	req1 := newShadowResultsRequest("bundle-A", fromMs, toMs, "summary")
	rec1 := httptest.NewRecorder()
	s.handleShadowResultsSummary(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call: %d %s", rec1.Code, rec1.Body.String())
	}
	var first ShadowResultsSummary
	_ = json.NewDecoder(rec1.Body).Decode(&first)
	if first.TotalEvaluated != 1 {
		t.Fatalf("first.Total=%d, want 1", first.TotalEvaluated)
	}

	// Wipe the stream.
	streamKey := shadowStreamKey("default")
	if err := s.redisClient().Del(context.Background(), streamKey).Err(); err != nil {
		t.Fatalf("del: %v", err)
	}

	// Second call with identical params must hit the cache (still 1).
	req2 := newShadowResultsRequest("bundle-A", fromMs, toMs, "summary")
	rec2 := httptest.NewRecorder()
	s.handleShadowResultsSummary(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second call: %d %s", rec2.Code, rec2.Body.String())
	}
	var second ShadowResultsSummary
	_ = json.NewDecoder(rec2.Body).Decode(&second)
	if second.TotalEvaluated != 1 {
		t.Errorf("cache miss: second.Total=%d, want 1 (from cached result)", second.TotalEvaluated)
	}

	// A different window must bypass the cache (now reads the empty stream).
	req3 := newShadowResultsRequest("bundle-A", fromMs+1, toMs, "summary")
	rec3 := httptest.NewRecorder()
	s.handleShadowResultsSummary(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("third call: %d %s", rec3.Code, rec3.Body.String())
	}
	var third ShadowResultsSummary
	_ = json.NewDecoder(rec3.Body).Decode(&third)
	if third.TotalEvaluated != 0 {
		t.Errorf("different-window call must not hit cache; got Total=%d", third.TotalEvaluated)
	}
}

// Silence "imported and not used" on strconv if other tests drop their
// dependency on it; this keeps the file robust against incremental
// edits.
var _ = strconv.Itoa

// ---------------------------------------------------------------------------
// Comparisons handler tests
// ---------------------------------------------------------------------------

// TestHandleShadowResultsComparisons_PaginatesViaCursor seeds 75
// shadow_eval events for bundle-A and walks them with limit=30,
// asserting each page's NextCursor round-trips back into a subsequent
// call that continues from the correct position.
func TestHandleShadowResultsComparisons_PaginatesViaCursor(t *testing.T) {
	s, _, _ := newTestGateway(t)
	resetShadowSummaryCache()

	base := time.UnixMilli(1_700_000_200_000).UTC()
	const total = 75
	for i := range total {
		seedShadowEventViaGatewayClient(t, s, "default",
			shadowEvent(int64(i+1), base.Add(time.Duration(i)*time.Millisecond), "default", "bundle-A", "shadow-A1", "escalated", "allow", "deny"))
	}

	fromMs := base.Add(-time.Second).UnixMilli()
	toMs := base.Add(time.Minute).UnixMilli()
	cursor := ""
	seen := 0
	pages := 0
	seqsSeen := map[int64]bool{}
	for {
		pages++
		if pages > 10 {
			t.Fatalf("pagination exceeded 10 pages (runaway?), seen=%d", seen)
		}
		url := fmt.Sprintf("/api/v1/policy/bundles/bundle-A/shadow/results/comparisons?from=%d&to=%d&limit=30", fromMs, toMs)
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.SetPathValue("id", "bundle-A")
		req = adminCtx(req)
		rec := httptest.NewRecorder()
		s.handleShadowResultsComparisons(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp ShadowComparisonsResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Entries) == 0 {
			break
		}
		seen += len(resp.Entries)
		for _, e := range resp.Entries {
			if seqsSeen[e.Seq] {
				t.Errorf("duplicate seq across pages: %d", e.Seq)
			}
			seqsSeen[e.Seq] = true
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	if seen != total {
		t.Errorf("paginated total = %d, want %d", seen, total)
	}
}

// TestHandleShadowResultsComparisons_DiffFilter narrows results to the
// requested diff class and drops the rest. Pins the over-scan-factor
// behaviour — a page of 10 `escalated` must return even when the
// surrounding events are `unchanged`.
func TestHandleShadowResultsComparisons_DiffFilter(t *testing.T) {
	s, _, _ := newTestGateway(t)
	resetShadowSummaryCache()

	base := time.UnixMilli(1_700_000_300_000).UTC()
	// Interleave: escalated, unchanged, escalated, unchanged, ...  for 40 total.
	seq := int64(0)
	for i := range 40 {
		diff := "escalated"
		if i%2 == 1 {
			diff = "unchanged"
		}
		seq++
		seedShadowEventViaGatewayClient(t, s, "default",
			shadowEvent(seq, base.Add(time.Duration(i)*time.Millisecond), "default", "bundle-A", "shadow-A1", diff, "allow", "deny"))
	}

	fromMs := base.Add(-time.Second).UnixMilli()
	toMs := base.Add(time.Minute).UnixMilli()
	url := fmt.Sprintf("/api/v1/policy/bundles/bundle-A/shadow/results/comparisons?from=%d&to=%d&limit=5&diff=escalated", fromMs, toMs)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", "bundle-A")
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsComparisons(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ShadowComparisonsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 5 {
		t.Fatalf("want 5 entries post-filter, got %d", len(resp.Entries))
	}
	for _, e := range resp.Entries {
		if e.Diff != "escalated" {
			t.Errorf("diff filter leaked %q", e.Diff)
		}
	}
}

// TestHandleShadowResultsComparisons_TenantIsolation seeds events
// under tenant-a and calls the handler as a caller with tenant-b
// scope. Events must not leak. Guards the hard rail that cross-tenant
// audit data stays sealed behind tenant claims.
func TestHandleShadowResultsComparisons_TenantIsolation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	resetShadowSummaryCache()
	base := time.UnixMilli(1_700_000_400_000).UTC()

	// Seed tenant-a events via its own stream key (audit:chain:tenant-a).
	for i := range 5 {
		ev := shadowEvent(int64(i+1), base.Add(time.Duration(i)*time.Millisecond), "tenant-a", "bundle-A", "shadow-A1", "escalated", "allow", "deny")
		raw, _ := json.Marshal(&ev)
		if _, err := s.redisClient().XAdd(context.Background(), &redis.XAddArgs{
			Stream: shadowStreamKey("tenant-a"),
			ID:     fmt.Sprintf("%d-%d", ev.Timestamp.UnixMilli(), ev.Seq),
			Values: map[string]any{"event": string(raw), "seq": fmt.Sprintf("%d", ev.Seq)},
		}).Result(); err != nil {
			t.Fatalf("xadd: %v", err)
		}
	}

	// Caller scope is "default" (adminCtx). The handler resolves tenant
	// via ctx — tenant-a events are in a different stream and must not
	// surface.
	fromMs := base.Add(-time.Second).UnixMilli()
	toMs := base.Add(time.Minute).UnixMilli()
	url := fmt.Sprintf("/api/v1/policy/bundles/bundle-A/shadow/results/comparisons?from=%d&to=%d", fromMs, toMs)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", "bundle-A")
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsComparisons(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ShadowComparisonsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("cross-tenant leak: got %d entries, want 0", len(resp.Entries))
	}
}

// TestHandleShadowResultsComparisons_BadDiffRejected and
// TestHandleShadowResultsComparisons_BadLimitRejected cover the 400
// validation surface for the two caller-provided knobs.
func TestHandleShadowResultsComparisons_BadDiffRejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	url := "/api/v1/policy/bundles/b/shadow/results/comparisons?from=1&to=2&diff=ohno"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", "b")
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsComparisons(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleShadowResultsComparisons_BadLimitRejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	url := "/api/v1/policy/bundles/b/shadow/results/comparisons?from=1&to=2&limit=1000"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", "b")
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsComparisons(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (limit > max)", rec.Code)
	}
}

// TestHandleShadowResultsComparisons_BadCursorRejected pins the
// cursor-validation surface so a malformed cursor never reaches
// Redis's XRANGE (which would 500 with a cryptic protocol error).
// ---------------------------------------------------------------------------
// Timeseries handler tests
// ---------------------------------------------------------------------------

// TestHandleShadowResultsTimeseries_BucketsAndZeroFill seeds events
// across 3 hourly boundaries, calls the handler with bucket=1h over a
// 5-hour window, and asserts:
//   - 5 buckets are returned (zero-filled where no event fell)
//   - counts in populated buckets match the seed
//   - buckets come back in ascending ts order
func TestHandleShadowResultsTimeseries_BucketsAndZeroFill(t *testing.T) {
	s, _, _ := newTestGateway(t)
	resetShadowSummaryCache()

	base := time.UnixMilli(1_700_000_500_000).UTC().Truncate(time.Hour)
	// Bucket 0: 2 escalated + 1 unchanged
	// Bucket 1: (empty — zero-filled)
	// Bucket 2: 1 relaxed + 1 approval_differ
	// Buckets 3 and 4: empty
	seq := int64(0)
	seedAt := func(offset time.Duration, diff string) {
		seq++
		seedShadowEventViaGatewayClient(t, s, "default",
			shadowEvent(seq, base.Add(offset), "default", "bundle-A", "shadow-A1", diff, "allow", "deny"))
	}
	seedAt(10*time.Minute, "escalated")
	seedAt(20*time.Minute, "escalated")
	seedAt(30*time.Minute, "unchanged")
	seedAt(2*time.Hour+5*time.Minute, "relaxed")
	seedAt(2*time.Hour+30*time.Minute, "approval_differ")

	fromMs := base.UnixMilli()
	toMs := base.Add(5 * time.Hour).UnixMilli()
	url := fmt.Sprintf("/api/v1/policy/bundles/bundle-A/shadow/results/timeseries?from=%d&to=%d&bucket=1h", fromMs, toMs)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", "bundle-A")
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsTimeseries(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ShadowTimeseriesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Bucket != "1h" {
		t.Errorf("Bucket echoed = %q", resp.Bucket)
	}
	if len(resp.Buckets) != 5 {
		t.Fatalf("want 5 buckets, got %d: %+v", len(resp.Buckets), resp.Buckets)
	}
	// Bucket 0: 3 total (2 escalated + 1 unchanged)
	if b := resp.Buckets[0]; b.Total != 3 || b.Escalated != 2 || b.Unchanged != 1 {
		t.Errorf("bucket[0] = %+v, want Total=3 Escalated=2 Unchanged=1", b)
	}
	// Bucket 1: zero-filled
	if b := resp.Buckets[1]; b.Total != 0 {
		t.Errorf("bucket[1] not zero-filled: %+v", b)
	}
	// Bucket 2: 2 total (1 relaxed + 1 approval_differ)
	if b := resp.Buckets[2]; b.Total != 2 || b.Relaxed != 1 || b.ApprovalDiffer != 1 {
		t.Errorf("bucket[2] = %+v, want Total=2 Relaxed=1 ApprovalDiffer=1", b)
	}
	// Buckets 3, 4: zero-filled
	for _, i := range []int{3, 4} {
		if b := resp.Buckets[i]; b.Total != 0 {
			t.Errorf("bucket[%d] not zero-filled: %+v", i, b)
		}
	}
	// Monotonic ts_ms (sanity).
	for i := 1; i < len(resp.Buckets); i++ {
		if resp.Buckets[i].TsMs <= resp.Buckets[i-1].TsMs {
			t.Errorf("buckets not monotonic at i=%d", i)
		}
	}
}

// TestHandleShadowResultsTimeseries_BadBucketRejected pins the bucket
// whitelist — arbitrary durations must 400 rather than quietly
// allocating a huge response.
func TestHandleShadowResultsTimeseries_BadBucketRejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	url := "/api/v1/policy/bundles/b/shadow/results/timeseries?from=1&to=2&bucket=30s"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", "b")
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsTimeseries(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

// TestHandleShadowResultsTimeseries_TooManyBucketsRejected guards the
// response-size budget. A 1-minute bucket over a 25-day range yields
// 36,000 buckets — the handler must 400 rather than OOM on JSON marshal.
func TestHandleShadowResultsTimeseries_TooManyBucketsRejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	fromMs := int64(1_700_000_000_000)
	toMs := fromMs + (25 * 24 * time.Hour).Milliseconds() // 25 days
	url := fmt.Sprintf("/api/v1/policy/bundles/b/shadow/results/timeseries?from=%d&to=%d&bucket=1m", fromMs, toMs)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", "b")
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsTimeseries(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (too many buckets)", rec.Code)
	}
}

func TestHandleShadowResultsComparisons_BadCursorRejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	url := "/api/v1/policy/bundles/b/shadow/results/comparisons?from=1&to=2&cursor=not-a-stream-id"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", "b")
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsComparisons(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (bad cursor)", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Coverage round-outs (step-6)
// ---------------------------------------------------------------------------

// viewerCtx attaches an authenticated non-admin context so we can
// exercise the 403 path. Mirrors adminCtx from handlers_locks_test.go.
func viewerCtx(req *http.Request) *http.Request {
	authCtx := &auth.AuthContext{Role: "viewer", Tenant: "default"}
	return req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
}

// TestShadowResults_EmptyStoreHandlers covers the empty-input baseline
// for all three handlers — dashboards rely on the zero-value shapes.
func TestShadowResults_EmptyStoreHandlers(t *testing.T) {
	s, _, _ := newTestGateway(t)
	resetShadowSummaryCache()
	fromMs := int64(1_700_000_600_000)
	toMs := fromMs + (time.Hour).Milliseconds()

	t.Run("summary returns zero counts", func(t *testing.T) {
		req := newShadowResultsRequest("empty-bundle", fromMs, toMs, "summary")
		rec := httptest.NewRecorder()
		s.handleShadowResultsSummary(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp ShadowResultsSummary
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if resp.TotalEvaluated != 0 || resp.EscalatedCount != 0 || resp.RelaxedCount != 0 || resp.ApprovalDifferCount != 0 || resp.UnchangedCount != 0 {
			t.Errorf("empty summary not all-zero: %+v", resp)
		}
		if resp.BundleID != "empty-bundle" {
			t.Errorf("BundleID echo wrong: %q", resp.BundleID)
		}
	})

	t.Run("comparisons returns empty entries", func(t *testing.T) {
		req := newShadowResultsRequest("empty-bundle", fromMs, toMs, "comparisons")
		rec := httptest.NewRecorder()
		s.handleShadowResultsComparisons(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp ShadowComparisonsResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if len(resp.Entries) != 0 {
			t.Errorf("empty comparisons: %d entries", len(resp.Entries))
		}
		if resp.NextCursor != "" {
			t.Errorf("empty comparisons NextCursor = %q, want empty", resp.NextCursor)
		}
	})

	t.Run("timeseries returns zero-filled buckets", func(t *testing.T) {
		// Snap from/to onto the bucket boundary so the expected bucket
		// count is deterministic regardless of how alignedFromMs rounds.
		const bucketMs = int64(15 * time.Minute / time.Millisecond)
		alignedFrom := (fromMs / bucketMs) * bucketMs
		alignedTo := alignedFrom + 4*bucketMs // 4 whole 15m buckets
		url := fmt.Sprintf("/api/v1/policy/bundles/empty-bundle/shadow/results/timeseries?from=%d&to=%d&bucket=15m", alignedFrom, alignedTo)
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.SetPathValue("id", "empty-bundle")
		req = adminCtx(req)
		rec := httptest.NewRecorder()
		s.handleShadowResultsTimeseries(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp ShadowTimeseriesResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if len(resp.Buckets) != 4 {
			t.Fatalf("want 4 zero-filled buckets, got %d", len(resp.Buckets))
		}
		for i, b := range resp.Buckets {
			if b.Total != 0 {
				t.Errorf("bucket[%d] not zero: %+v", i, b)
			}
		}
	})
}

// TestShadowResults_NonAdmin403 covers the admin gate on all three
// handlers. Pinning the role check means a future auth refactor
// can't silently expose shadow data to non-admin viewers. Installs
// roleEnforcingAuth (from handlers_policy_shadow_test.go) so that
// requireRole actually rejects instead of pass-through on nil auth.
func TestShadowResults_NonAdmin403(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = roleEnforcingAuth{}
	fromMs := int64(1_700_000_700_000)
	toMs := fromMs + time.Hour.Milliseconds()

	cases := []struct {
		name string
		call http.HandlerFunc
		sub  string
	}{
		{"summary", s.handleShadowResultsSummary, "summary"},
		{"comparisons", s.handleShadowResultsComparisons, "comparisons"},
		{"timeseries", s.handleShadowResultsTimeseries, "timeseries"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var url string
			if tc.sub == "timeseries" {
				url = fmt.Sprintf("/api/v1/policy/bundles/b/shadow/results/%s?from=%d&to=%d&bucket=1h", tc.sub, fromMs, toMs)
			} else {
				url = fmt.Sprintf("/api/v1/policy/bundles/b/shadow/results/%s?from=%d&to=%d", tc.sub, fromMs, toMs)
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.SetPathValue("id", "b")
			req = viewerCtx(req)
			rec := httptest.NewRecorder()
			tc.call(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("non-admin status=%d, want 403", rec.Code)
			}
		})
	}
}

// TestShadowResults_CrossTenantLeakageSealed mirrors the comparisons
// cross-tenant test but for the summary and timeseries endpoints —
// all three projections must be tenant-sealed.
func TestShadowResults_CrossTenantLeakageSealed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	resetShadowSummaryCache()
	base := time.UnixMilli(1_700_000_800_000).UTC()

	// Seed shadow_eval events under tenant-a.
	for i := range 5 {
		ev := shadowEvent(int64(i+1), base.Add(time.Duration(i)*time.Millisecond), "tenant-a", "bundle-A", "shadow-A1", "escalated", "allow", "deny")
		raw, _ := json.Marshal(&ev)
		if _, err := s.redisClient().XAdd(context.Background(), &redis.XAddArgs{
			Stream: shadowStreamKey("tenant-a"),
			ID:     fmt.Sprintf("%d-%d", ev.Timestamp.UnixMilli(), ev.Seq),
			Values: map[string]any{"event": string(raw), "seq": fmt.Sprintf("%d", ev.Seq)},
		}).Result(); err != nil {
			t.Fatalf("xadd: %v", err)
		}
	}

	fromMs := base.Add(-time.Second).UnixMilli()
	toMs := base.Add(time.Minute).UnixMilli()

	t.Run("summary", func(t *testing.T) {
		req := newShadowResultsRequest("bundle-A", fromMs, toMs, "summary")
		rec := httptest.NewRecorder()
		s.handleShadowResultsSummary(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp ShadowResultsSummary
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if resp.TotalEvaluated != 0 {
			t.Errorf("cross-tenant leak into summary: TotalEvaluated=%d", resp.TotalEvaluated)
		}
	})

	t.Run("timeseries", func(t *testing.T) {
		url := fmt.Sprintf("/api/v1/policy/bundles/bundle-A/shadow/results/timeseries?from=%d&to=%d&bucket=1m", fromMs, toMs)
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.SetPathValue("id", "bundle-A")
		req = adminCtx(req)
		rec := httptest.NewRecorder()
		s.handleShadowResultsTimeseries(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp ShadowTimeseriesResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		for i, b := range resp.Buckets {
			if b.Total != 0 {
				t.Errorf("bucket[%d] leaked cross-tenant event: %+v", i, b)
			}
		}
	})
}

// TestShadowResults_MissingBundleIDReturns400 guards the path
// extraction — a mux quirk or an internal caller passing an empty
// PathValue must surface as 400 rather than silently scanning every
// bundle_id for that tenant.
func TestShadowResults_MissingBundleIDReturns400(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles//shadow/results/summary?from=1&to=2", nil)
	req.SetPathValue("id", "") // simulate an empty path segment
	req = adminCtx(req)
	rec := httptest.NewRecorder()
	s.handleShadowResultsSummary(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (empty bundle id)", rec.Code)
	}
}
