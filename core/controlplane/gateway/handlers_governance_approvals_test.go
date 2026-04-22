package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
)

// approvalAnalyticsStore lets the test drive both the decision log +
// the approval-record lookup through one stub so fixtures stay
// readable. The real server wires decisionLogStore + jobStore
// separately; we splice the jobStore behaviour via a dedicated
// override below.
type approvalAnalyticsStore struct {
	stubDecisionLogStore
}

// writeApprovalRecords primes the test's jobStore-equivalent. We
// can't swap jobStore out in the existing harness (it is a concrete
// *store.RedisJobStore), so the test uses the memStore/redis setup
// that newTestGateway provisions and writes approval records via
// the underlying SetApprovalRecord API.

func TestParseApprovalAnalyticsQuery_Defaults(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/governance/approvals/analytics", nil)
	q, err := parseApprovalAnalyticsQuery(req, now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if q.GroupBy != "overall" {
		t.Fatalf("group_by = %q, want overall", q.GroupBy)
	}
	wantSince := now.Add(-24 * time.Hour).UnixMilli()
	if q.Since != wantSince {
		t.Fatalf("since = %d, want %d (-24h)", q.Since, wantSince)
	}
	if q.Until != now.UnixMilli() {
		t.Fatalf("until = %d, want %d (now)", q.Until, now.UnixMilli())
	}
	if q.Limit != approvalAnalyticsDefaultLimit {
		t.Fatalf("limit = %d, want default %d", q.Limit, approvalAnalyticsDefaultLimit)
	}
}

func TestParseApprovalAnalyticsQuery_WindowShorthand(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/governance/approvals/analytics?window=7d&group_by=rule&limit=25", nil)
	q, err := parseApprovalAnalyticsQuery(req, now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if q.GroupBy != "rule" {
		t.Fatalf("group_by = %q, want rule", q.GroupBy)
	}
	wantSince := now.Add(-7 * 24 * time.Hour).UnixMilli()
	if q.Since != wantSince {
		t.Fatalf("since = %d, want %d", q.Since, wantSince)
	}
	if q.Limit != 25 {
		t.Fatalf("limit = %d, want 25", q.Limit)
	}
}

func TestParseApprovalAnalyticsQuery_RejectsBadParams(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name string
		url  string
	}{
		{"bad group_by", "/x?group_by=sideways"},
		{"bad window shorthand", "/x?window=5m"},
		{"limit over max", "/x?limit=99"},
		{"limit zero", "/x?limit=0"},
		{"limit negative", "/x?limit=-1"},
		{"limit non-numeric", "/x?limit=abc"},
		{"until before since", "/x?since=1700000000000&until=1600000000000"},
		{"window > 30d", "/x?since=1600000000000&until=1700000000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.url, nil)
			if _, err := parseApprovalAnalyticsQuery(req, now); err == nil {
				t.Fatalf("expected error for %s", c.name)
			}
		})
	}
}

func TestApprovalAnalyticsCacheKey_DeterministicPerInput(t *testing.T) {
	a := approvalAnalyticsCacheKey("tenant-x", approvalAnalyticsQuery{
		Since: 100, Until: 200, GroupBy: "rule", Limit: 10,
	})
	b := approvalAnalyticsCacheKey("tenant-x", approvalAnalyticsQuery{
		Since: 100, Until: 200, GroupBy: "rule", Limit: 10,
	})
	if a != b {
		t.Fatalf("cache key drifted across identical inputs: %s vs %s", a, b)
	}
	c := approvalAnalyticsCacheKey("tenant-y", approvalAnalyticsQuery{
		Since: 100, Until: 200, GroupBy: "rule", Limit: 10,
	})
	if a == c {
		t.Fatal("cache key collided across tenants")
	}
}

func TestApprovalAnalyticsCache_TTLExpires(t *testing.T) {
	cache := &approvalAnalyticsMemCache{
		ttl:     10 * time.Millisecond,
		entries: map[string]approvalAnalyticsCacheEntry{},
	}
	cache.set("k", approvalAnalyticsResponse{Summary: approvalAnalyticsSummary{Total: 5}})
	if v, ok := cache.get("k"); !ok || v.Summary.Total != 5 {
		t.Fatal("cache miss on immediate read")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := cache.get("k"); ok {
		t.Fatal("cache did not expire after TTL")
	}
}

func TestSummariseApprovalSamples_EmptyYieldsNullPercentiles(t *testing.T) {
	sum := summariseApprovalSamples(nil)
	if sum.Total != 0 || sum.Approved != 0 {
		t.Fatalf("zero samples should yield zero counters, got %+v", sum)
	}
	// Null-vs-zero distinction is load-bearing for the widget — it
	// needs to render "no data" rather than a misleading "0 s avg".
	if sum.AvgTimeToApproveSeconds != nil || sum.P50 != nil || sum.P90 != nil || sum.P99 != nil {
		t.Fatalf("percentiles must be nil on empty sample set, got %+v", sum)
	}
}

func TestSummariseApprovalSamples_CountsAndPercentiles(t *testing.T) {
	base := int64(1_700_000_000_000) // arbitrary anchor
	samples := []approvalSample{
		// 3 approved, 1 rejected, 1 expired (auto), 1 still pending.
		mkSample(base, base+10_000, model.ApprovalDecisionApprove, "rule-a", "agent-x", "topic.1"),
		mkSample(base, base+30_000, model.ApprovalDecisionApprove, "rule-a", "agent-x", "topic.1"),
		mkSample(base, base+50_000, model.ApprovalDecisionApprove, "rule-b", "agent-y", "topic.2"),
		mkSample(base, base+120_000, model.ApprovalDecisionReject, "rule-c", "agent-z", "topic.3"),
		mkSample(base, base+600_000, model.ApprovalDecisionExpire, "rule-c", "agent-z", "topic.3"),
		mkPendingSample(base, "rule-d", "agent-x", "topic.1"),
	}
	sum := summariseApprovalSamples(samples)
	if sum.Total != 6 {
		t.Fatalf("total = %d, want 6", sum.Total)
	}
	if sum.Approved != 3 {
		t.Fatalf("approved = %d, want 3", sum.Approved)
	}
	if sum.Rejected != 1 {
		t.Fatalf("rejected = %d, want 1", sum.Rejected)
	}
	if sum.Expired != 1 {
		t.Fatalf("expired = %d, want 1", sum.Expired)
	}
	// Expire counts as auto; approve/reject count as manual.
	if sum.AutoResolved != 1 {
		t.Fatalf("auto_resolved = %d, want 1", sum.AutoResolved)
	}
	if sum.ManualResolved != 4 {
		t.Fatalf("manual_resolved = %d, want 4", sum.ManualResolved)
	}
	if sum.AvgTimeToApproveSeconds == nil {
		t.Fatal("avg_ttar should be non-nil when samples resolved")
	}
	// TTARs are 10s, 30s, 50s, 120s, 600s — avg ~162s.
	if *sum.AvgTimeToApproveSeconds < 100 || *sum.AvgTimeToApproveSeconds > 200 {
		t.Fatalf("avg_ttar out of expected range: %f", *sum.AvgTimeToApproveSeconds)
	}
	if sum.P50 == nil || sum.P90 == nil || sum.P99 == nil {
		t.Fatal("percentiles must be non-nil when samples present")
	}
}

func TestGroupApprovalSamples_SortedByAvgTTARDesc(t *testing.T) {
	base := int64(1_700_000_000_000)
	samples := []approvalSample{
		mkSample(base, base+10_000, model.ApprovalDecisionApprove, "rule-fast", "a", ""),
		mkSample(base, base+20_000, model.ApprovalDecisionApprove, "rule-fast", "a", ""),
		mkSample(base, base+900_000, model.ApprovalDecisionApprove, "rule-slow", "b", ""),
		mkSample(base, base+1_100_000, model.ApprovalDecisionApprove, "rule-slow", "b", ""),
	}
	groups := groupApprovalSamples(samples, "rule", 10)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].Key != "rule-slow" {
		t.Fatalf("expected slowest group first, got %+v", groups)
	}
	if groups[0].AvgTTARSeconds == nil || *groups[0].AvgTTARSeconds < *groups[1].AvgTTARSeconds {
		t.Fatalf("sort order wrong: %+v", groups)
	}
}

func TestGroupApprovalSamples_TopNCap(t *testing.T) {
	base := int64(1_700_000_000_000)
	// 5 rules with distinct avg ttar; limit=2 keeps the 2 worst.
	samples := []approvalSample{
		mkSample(base, base+10_000, model.ApprovalDecisionApprove, "r1", "a", ""),
		mkSample(base, base+20_000, model.ApprovalDecisionApprove, "r2", "a", ""),
		mkSample(base, base+30_000, model.ApprovalDecisionApprove, "r3", "a", ""),
		mkSample(base, base+40_000, model.ApprovalDecisionApprove, "r4", "a", ""),
		mkSample(base, base+50_000, model.ApprovalDecisionApprove, "r5", "a", ""),
	}
	groups := groupApprovalSamples(samples, "rule", 2)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2 (limit)", len(groups))
	}
	if groups[0].Key != "r5" || groups[1].Key != "r4" {
		t.Fatalf("top-2 by slowest: got %s,%s", groups[0].Key, groups[1].Key)
	}
}

func TestApprovalAnalyticsEndpoint_EmptyWindowReturnsZeros(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &approvalAnalyticsStore{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/governance/approvals/analytics", nil)
	req = withAuth(req, &auth.AuthContext{Tenant: "default", PrincipalID: "u", Role: "admin", AllowCrossTenant: true})
	rec := httptest.NewRecorder()
	s.handleApprovalAnalytics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body approvalAnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Summary.Total != 0 {
		t.Fatalf("total = %d on empty window, want 0", body.Summary.Total)
	}
	if body.Summary.AvgTimeToApproveSeconds != nil {
		t.Fatal("avg_ttar must be null on empty window")
	}
}

func TestApprovalAnalyticsEndpoint_BadGroupBy(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &approvalAnalyticsStore{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/governance/approvals/analytics?group_by=sideways", nil)
	req = withAuth(req, &auth.AuthContext{Tenant: "default", PrincipalID: "u", Role: "admin", AllowCrossTenant: true})
	rec := httptest.NewRecorder()
	s.handleApprovalAnalytics(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid group_by") {
		t.Fatalf("body missing reason: %s", rec.Body.String())
	}
}

func TestApprovalAnalyticsEndpoint_ServiceUnavailableWhenNoStore(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = nil
	req := httptest.NewRequest(http.MethodGet, "/api/v1/governance/approvals/analytics", nil)
	req = withAuth(req, &auth.AuthContext{Tenant: "default", PrincipalID: "u", Role: "admin", AllowCrossTenant: true})
	rec := httptest.NewRecorder()
	s.handleApprovalAnalytics(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// mkSample synthesises a fixed-shape sample. approvedAtMS is the
// resolution timestamp; the decision timestamp is `decidedAtMS`.
func mkSample(decidedAtMS, approvedAtMS int64, decision model.ApprovalDecision, rule, agent, topic string) approvalSample {
	return approvalSample{
		record: model.DecisionLogRecord{
			JobID:     "job-" + rule,
			RuleID:    rule,
			AgentID:   agent,
			Topic:     topic,
			Verdict:   model.SafetyRequireApproval,
			Timestamp: decidedAtMS,
		},
		approval:    model.ApprovalRecord{Decision: decision, ApprovedAt: approvedAtMS},
		hasApproval: true,
	}
}

func mkPendingSample(decidedAtMS int64, rule, agent, topic string) approvalSample {
	return approvalSample{
		record: model.DecisionLogRecord{
			JobID:     "pending-" + rule,
			RuleID:    rule,
			AgentID:   agent,
			Topic:     topic,
			Verdict:   model.SafetyRequireApproval,
			Timestamp: decidedAtMS,
		},
		hasApproval: false,
	}
}

// ensure unused-import guard doesn't remove context when the stub
// pattern is used.
var _ = context.Background
