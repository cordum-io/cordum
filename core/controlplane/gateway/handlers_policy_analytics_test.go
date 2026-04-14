package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func analyticsRequest(t *testing.T, s *server, body map[string]any, auth *AuthContext) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/analytics", bytes.NewReader(payload))
	tenant := "default"
	if auth != nil && auth.Tenant != "" {
		tenant = auth.Tenant
	}
	req.Header.Set("X-Tenant-ID", tenant)
	if auth != nil {
		req = withAuth(req, auth)
	}
	rec := httptest.NewRecorder()
	s.handlePolicyAnalytics(rec, req)
	return rec
}

func decodeAnalyticsResponse(t *testing.T, rec *httptest.ResponseRecorder) policyAnalyticsResponse {
	t.Helper()
	var resp policyAnalyticsResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err, "decode analytics response")
	return resp
}

func seedAnalyticsJobs(t *testing.T, s *server, jobs []analyticsTestJob) {
	t.Helper()
	ctx := context.Background()
	for _, j := range jobs {
		jobReq := &pb.JobRequest{
			JobId:       j.ID,
			Topic:       j.Topic,
			TenantId:    j.Tenant,
			PrincipalId: "test-principal",
			Meta: &pb.JobMetadata{
				TenantId: j.Tenant,
			},
		}
		require.NoError(t, s.jobStore.SetJobMeta(ctx, jobReq), "set meta for %s", j.ID)
		require.NoError(t, s.jobStore.SetJobRequest(ctx, jobReq), "set request for %s", j.ID)
		require.NoError(t, s.jobStore.SetState(ctx, j.ID, model.JobStatePending), "set state for %s", j.ID)

		if j.SafetyDecision != "" || j.SafetyRuleID != "" {
			sd := model.SafetyDecisionRecord{
				Decision: model.SafetyDecision(j.SafetyDecision),
				RuleID:   j.SafetyRuleID,
			}
			if j.SafetyCheckedAt > 0 {
				sd.CheckedAt = j.SafetyCheckedAt
			}
			require.NoError(t, s.jobStore.SetSafetyDecision(ctx, j.ID, sd), "set safety decision for %s", j.ID)
		}

		if j.ApprovalBy != "" {
			require.NoError(t, s.jobStore.SetApprovalRecord(ctx, j.ID, model.ApprovalRecord{
				ApprovedBy: j.ApprovalBy,
				ApprovedAt: j.ApprovalAt,
				Decision:   model.ApprovalDecisionApprove,
				Status:     model.ApprovalStatusApproved,
			}), "set approval record for %s", j.ID)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

type analyticsTestJob struct {
	ID              string
	Topic           string
	Tenant          string
	SafetyDecision  string
	SafetyRuleID    string
	SafetyCheckedAt int64
	ApprovalBy      string
	ApprovalAt      int64
}

// ---------- Tests ----------

func TestPolicyAnalytics_Forbidden(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}

	now := time.Now().UTC()
	rec := analyticsRequest(t, s, map[string]any{
		"from": now.Add(-24 * time.Hour).Format(time.RFC3339),
		"to":   now.Format(time.RFC3339),
	}, &AuthContext{Role: "viewer"})

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestPolicyAnalytics_BadTimeRange(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	admin := &AuthContext{Role: "admin", Tenant: "default", PrincipalID: "admin-1"}

	// from >= to
	now := time.Now().UTC()
	rec := analyticsRequest(t, s, map[string]any{
		"from": now.Format(time.RFC3339),
		"to":   now.Add(-1 * time.Hour).Format(time.RFC3339),
	}, admin)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Exceeds 7 days
	rec = analyticsRequest(t, s, map[string]any{
		"from": now.Add(-8 * 24 * time.Hour).Format(time.RFC3339),
		"to":   now.Format(time.RFC3339),
	}, admin)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPolicyAnalytics_EmptyResult(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	admin := &AuthContext{Role: "admin", Tenant: "default", PrincipalID: "admin-1"}

	now := time.Now().UTC()
	rec := analyticsRequest(t, s, map[string]any{
		"from": now.Add(-24 * time.Hour).Format(time.RFC3339),
		"to":   now.Format(time.RFC3339),
	}, admin)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAnalyticsResponse(t, rec)
	assert.Len(t, resp.Rules, 0, "no rules when no jobs exist")
	assert.Equal(t, 0, resp.Summary.TotalHits)
}

func TestPolicyAnalytics_PerRuleMetrics(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	admin := &AuthContext{Role: "admin", Tenant: "acme", PrincipalID: "admin-1"}

	now := time.Now().UTC()
	checkedAt := now.Add(-1 * time.Hour).UnixMicro()
	approvedAt := now.Add(-30 * time.Minute).UnixMicro() // 30 min latency

	seedAnalyticsJobs(t, s, []analyticsTestJob{
		// Rule A: 3 hits, 2 require_approval, 1 overridden
		{ID: "job-a1", Topic: "deploy", Tenant: "acme", SafetyDecision: "REQUIRE_APPROVAL", SafetyRuleID: "rule-A", SafetyCheckedAt: checkedAt, ApprovalBy: "admin@acme.com", ApprovalAt: approvedAt},
		{ID: "job-a2", Topic: "deploy", Tenant: "acme", SafetyDecision: "REQUIRE_APPROVAL", SafetyRuleID: "rule-A"},
		{ID: "job-a3", Topic: "deploy", Tenant: "acme", SafetyDecision: "ALLOW", SafetyRuleID: "rule-A"},
		// Rule B: 2 hits, 2 require_approval, 2 overridden
		{ID: "job-b1", Topic: "billing", Tenant: "acme", SafetyDecision: "REQUIRE_APPROVAL", SafetyRuleID: "rule-B", SafetyCheckedAt: checkedAt, ApprovalBy: "mgr@acme.com", ApprovalAt: approvedAt},
		{ID: "job-b2", Topic: "billing", Tenant: "acme", SafetyDecision: "REQUIRE_APPROVAL", SafetyRuleID: "rule-B", SafetyCheckedAt: checkedAt, ApprovalBy: "cfo@acme.com", ApprovalAt: approvedAt},
	})

	rec := analyticsRequest(t, s, map[string]any{
		"from": now.Add(-2 * time.Hour).Format(time.RFC3339),
		"to":   now.Add(1 * time.Hour).Format(time.RFC3339),
	}, admin)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAnalyticsResponse(t, rec)
	assert.Len(t, resp.Rules, 2, "should have 2 distinct rules")

	// Find rule-A and rule-B.
	ruleMap := map[string]ruleAnalytics{}
	for _, r := range resp.Rules {
		ruleMap[r.RuleID] = r
	}

	ruleA := ruleMap["rule-A"]
	assert.Equal(t, 3, ruleA.HitCount, "rule-A hit count")
	assert.Equal(t, 2, ruleA.ApprovalCount, "rule-A approval count")
	assert.Equal(t, 1, ruleA.OverrideCount, "rule-A override count")
	assert.InDelta(t, 0.5, ruleA.OverrideRate, 0.01, "rule-A override rate = 1/2")
	assert.Greater(t, ruleA.AvgApprovalLatencyMs, int64(0), "rule-A has latency data")

	ruleB := ruleMap["rule-B"]
	assert.Equal(t, 2, ruleB.HitCount, "rule-B hit count")
	assert.Equal(t, 2, ruleB.ApprovalCount, "rule-B approval count")
	assert.Equal(t, 2, ruleB.OverrideCount, "rule-B override count")
	assert.InDelta(t, 1.0, ruleB.OverrideRate, 0.01, "rule-B override rate = 2/2")

	// Summary
	assert.Equal(t, 2, resp.Summary.TotalRules)
	assert.Equal(t, 5, resp.Summary.TotalHits)
	assert.Equal(t, 3, resp.Summary.TotalOverrides)
	assert.Equal(t, "rule-B", resp.Summary.HighestOverrideRule, "rule-B has 100% override rate")
}

func TestPolicyAnalytics_RuleFilter(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	admin := &AuthContext{Role: "admin", Tenant: "acme", PrincipalID: "admin-1"}

	now := time.Now().UTC()
	seedAnalyticsJobs(t, s, []analyticsTestJob{
		{ID: "job-f1", Topic: "deploy", Tenant: "acme", SafetyDecision: "ALLOW", SafetyRuleID: "rule-X"},
		{ID: "job-f2", Topic: "deploy", Tenant: "acme", SafetyDecision: "DENY", SafetyRuleID: "rule-Y"},
	})

	rec := analyticsRequest(t, s, map[string]any{
		"from":        now.Add(-2 * time.Hour).Format(time.RFC3339),
		"to":          now.Add(1 * time.Hour).Format(time.RFC3339),
		"rule_filter": "rule-X",
	}, admin)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAnalyticsResponse(t, rec)
	assert.Len(t, resp.Rules, 1, "only filtered rule returned")
	assert.Equal(t, "rule-X", resp.Rules[0].RuleID)
}

func TestPolicyAnalytics_DailyHitsBucketing(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	admin := &AuthContext{Role: "admin", Tenant: "acme", PrincipalID: "admin-1"}

	now := time.Now().UTC()
	seedAnalyticsJobs(t, s, []analyticsTestJob{
		{ID: "job-d1", Topic: "deploy", Tenant: "acme", SafetyDecision: "ALLOW", SafetyRuleID: "rule-D"},
		{ID: "job-d2", Topic: "deploy", Tenant: "acme", SafetyDecision: "ALLOW", SafetyRuleID: "rule-D"},
	})

	rec := analyticsRequest(t, s, map[string]any{
		"from": now.Add(-3 * 24 * time.Hour).Format(time.RFC3339),
		"to":   now.Add(1 * time.Hour).Format(time.RFC3339),
	}, admin)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAnalyticsResponse(t, rec)
	require.Len(t, resp.Rules, 1)

	ruleD := resp.Rules[0]
	assert.Equal(t, "rule-D", ruleD.RuleID)
	assert.Len(t, ruleD.DailyHits, 4, "should have 4 daily buckets for ~3 day range")
	// All jobs were just created, so hits should be in the last bucket.
	totalFromBuckets := 0
	for _, h := range ruleD.DailyHits {
		totalFromBuckets += h
	}
	assert.Equal(t, 2, totalFromBuckets, "daily buckets should sum to hit count")
}

func TestPolicyAnalytics_OverrideRateCalculation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	admin := &AuthContext{Role: "admin", Tenant: "acme", PrincipalID: "admin-1"}

	now := time.Now().UTC()
	checkedAt := now.Add(-1 * time.Hour).UnixMicro()
	approvedAt := now.Add(-50 * time.Minute).UnixMicro() // 10 min latency

	// 3 approvals, 2 overridden = 0.667
	seedAnalyticsJobs(t, s, []analyticsTestJob{
		{ID: "job-or1", Topic: "fin", Tenant: "acme", SafetyDecision: "REQUIRE_APPROVAL", SafetyRuleID: "rule-OR", SafetyCheckedAt: checkedAt, ApprovalBy: "u1", ApprovalAt: approvedAt},
		{ID: "job-or2", Topic: "fin", Tenant: "acme", SafetyDecision: "REQUIRE_APPROVAL", SafetyRuleID: "rule-OR", SafetyCheckedAt: checkedAt, ApprovalBy: "u2", ApprovalAt: approvedAt},
		{ID: "job-or3", Topic: "fin", Tenant: "acme", SafetyDecision: "REQUIRE_APPROVAL", SafetyRuleID: "rule-OR"},
	})

	rec := analyticsRequest(t, s, map[string]any{
		"from": now.Add(-2 * time.Hour).Format(time.RFC3339),
		"to":   now.Add(1 * time.Hour).Format(time.RFC3339),
	}, admin)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAnalyticsResponse(t, rec)
	require.Len(t, resp.Rules, 1)

	rule := resp.Rules[0]
	assert.Equal(t, 3, rule.ApprovalCount)
	assert.Equal(t, 2, rule.OverrideCount)
	assert.InDelta(t, 0.667, rule.OverrideRate, 0.01, "2/3 = 0.667")
	assert.Greater(t, rule.AvgApprovalLatencyMs, int64(0), "should have latency")
}

func TestPolicyAnalytics_TenantIsolation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	admin := &AuthContext{Role: "admin", Tenant: "acme", PrincipalID: "admin-1"}

	now := time.Now().UTC()
	checkedAt := now.Add(-1 * time.Hour).UnixMicro()
	approvedAt := now.Add(-30 * time.Minute).UnixMicro()

	seedAnalyticsJobs(t, s, []analyticsTestJob{
		{ID: "job-tenant-a1", Topic: "deploy", Tenant: "acme", SafetyDecision: "REQUIRE_APPROVAL", SafetyRuleID: "rule-shared", SafetyCheckedAt: checkedAt, ApprovalBy: "admin@acme.com", ApprovalAt: approvedAt},
		{ID: "job-tenant-a2", Topic: "deploy", Tenant: "acme", SafetyDecision: "ALLOW", SafetyRuleID: "rule-shared"},
		{ID: "job-tenant-b1", Topic: "deploy", Tenant: "beta", SafetyDecision: "REQUIRE_APPROVAL", SafetyRuleID: "rule-shared", SafetyCheckedAt: checkedAt, ApprovalBy: "admin@beta.com", ApprovalAt: approvedAt},
		{ID: "job-tenant-b2", Topic: "billing", Tenant: "beta", SafetyDecision: "ALLOW", SafetyRuleID: "rule-beta"},
	})

	rec := analyticsRequest(t, s, map[string]any{
		"from": now.Add(-2 * time.Hour).Format(time.RFC3339),
		"to":   now.Add(1 * time.Hour).Format(time.RFC3339),
	}, admin)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAnalyticsResponse(t, rec)
	require.Len(t, resp.Rules, 1, "only caller tenant rules should be returned")

	rule := resp.Rules[0]
	assert.Equal(t, "rule-shared", rule.RuleID)
	assert.Equal(t, 2, rule.HitCount, "beta jobs must not leak into shared rule counts")
	assert.Equal(t, 1, rule.ApprovalCount)
	assert.Equal(t, 1, rule.OverrideCount)
	assert.Equal(t, 2, resp.Summary.TotalHits)
	assert.Equal(t, 1, resp.Summary.TotalOverrides)
}
