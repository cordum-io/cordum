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

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

func TestPolicyDecisionsHandlerReturnsEmpty200WhenStoreHasNoMatches(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)

	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/policy/decisions?tenant=default", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got policyDecisionsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.Empty(t, got.Items)
	require.False(t, got.HasMore)
	require.Empty(t, got.NextCursor)
}

func TestPolicyDecisionsHandlerReturnsAllUnifiedFields(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)

	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))

	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).UnixMilli()
	rec := unifiedDecisionRecordFixture("default", "job-1", now, policy.DecisionSourceJob, policy.DecisionDeny)
	rec.BundleID = "bundle-acme"
	rec.BundleVersion = "v3"
	rec.AuditHash = "sha256:abc"
	rec.InputRef = "blob://input-1"
	rec.OutputRef = "blob://output-1"
	rec.Trace = []policy.TraceStep{{
		RuleID:       rec.RuleID,
		BundleID:     "bundle-acme",
		DecisionType: policy.DecisionDeny,
		Reason:       "matched secret-leak",
		Timestamp:    time.UnixMilli(now).UTC(),
	}}
	require.NoError(t, s.decisionLogStore.AppendDecision(context.Background(), rec))

	req := adminCtx(httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/policy/decisions?tenant=default&since=%d&until=%d&limit=10", now-1000, now+1000), nil))
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, req)

	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	var got policyDecisionsResponse
	require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
	require.Len(t, got.Items, 1)
	d := got.Items[0]
	require.Equal(t, policy.DecisionSourceJob, d.Source)
	require.Equal(t, rec.RuleID, d.RuleID)
	require.Equal(t, "bundle-acme", d.BundleID)
	require.Equal(t, "v3", d.BundleVersion)
	require.Equal(t, policy.DecisionDeny, d.Type)
	require.Len(t, d.Trace, 1)
	require.Equal(t, policy.DecisionDeny, d.Trace[0].DecisionType)
	require.Equal(t, "blob://input-1", d.InputRef)
	require.Equal(t, "blob://output-1", d.OutputRef)
	require.Equal(t, "sha256:abc", d.AuditHash)
	require.False(t, d.Timestamp.IsZero())
}

func TestPolicyDecisionsHandlerFiltersBySource(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))

	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).UnixMilli()
	jobRec := unifiedDecisionRecordFixture("default", "job-J", base-3000, policy.DecisionSourceJob, policy.DecisionAllow)
	edgeRec := unifiedDecisionRecordFixture("default", "edge-E", base-2000, policy.DecisionSourceEdge, policy.DecisionDeny)
	require.NoError(t, s.decisionLogStore.AppendDecision(context.Background(), jobRec))
	require.NoError(t, s.decisionLogStore.AppendDecision(context.Background(), edgeRec))

	cases := []struct {
		source string
		want   policy.DecisionSource
	}{
		{"job", policy.DecisionSourceJob},
		{"edge", policy.DecisionSourceEdge},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			req := adminCtx(httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/api/v1/policy/decisions?tenant=default&source=%s&since=%d&until=%d&limit=50",
					tc.source, base-10_000, base), nil))
			r := httptest.NewRecorder()
			mux.ServeHTTP(r, req)

			require.Equal(t, http.StatusOK, r.Code, r.Body.String())
			var got policyDecisionsResponse
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
			require.Len(t, got.Items, 1, "source=%s expected exactly 1 item", tc.source)
			require.Equal(t, tc.want, got.Items[0].Source)
		})
	}
}

func TestPolicyDecisionsHandlerFiltersByType(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))

	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).UnixMilli()
	allow := unifiedDecisionRecordFixture("default", "j-allow", base-3000, policy.DecisionSourceJob, policy.DecisionAllow)
	deny := unifiedDecisionRecordFixture("default", "j-deny", base-2000, policy.DecisionSourceJob, policy.DecisionDeny)
	require.NoError(t, s.decisionLogStore.AppendDecision(context.Background(), allow))
	require.NoError(t, s.decisionLogStore.AppendDecision(context.Background(), deny))

	req := adminCtx(httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/policy/decisions?tenant=default&type=deny&since=%d&until=%d", base-10_000, base), nil))
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, req)

	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	var got policyDecisionsResponse
	require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
	require.Len(t, got.Items, 1)
	require.Equal(t, policy.DecisionDeny, got.Items[0].Type)
}

func TestPolicyDecisionsHandlerHonorsLimitAndAdvancesCursor(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))

	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).UnixMilli()
	const total = 12
	for i := 0; i < total; i++ {
		rec := unifiedDecisionRecordFixture("default",
			fmt.Sprintf("j-%02d", i),
			base-int64((total-i)*1000),
			policy.DecisionSourceJob, policy.DecisionAllow)
		require.NoError(t, s.decisionLogStore.AppendDecision(context.Background(), rec))
	}

	// First page: limit=5 → items[5], has_more=true, next_cursor!=""
	req := adminCtx(httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/policy/decisions?tenant=default&limit=5&since=%d&until=%d",
			base-100_000, base), nil))
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, req)
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	var first policyDecisionsResponse
	require.NoError(t, json.NewDecoder(r.Body).Decode(&first))
	require.Len(t, first.Items, 5)
	require.True(t, first.HasMore)
	require.NotEmpty(t, first.NextCursor)

	// Walk to exhaustion using cursors. No skipped/duplicated rows.
	seen := map[string]int{}
	for _, d := range first.Items {
		seen[d.RuleID]++
	}
	cursor := first.NextCursor
	pages := 1
	for cursor != "" && pages < 10 {
		req := adminCtx(httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/v1/policy/decisions?tenant=default&limit=5&cursor=%s&since=%d&until=%d",
				cursor, base-100_000, base), nil))
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, req)
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		var next policyDecisionsResponse
		require.NoError(t, json.NewDecoder(r.Body).Decode(&next))
		for _, d := range next.Items {
			seen[d.RuleID]++
		}
		cursor = next.NextCursor
		pages++
	}
	require.Equal(t, total, len(seen),
		"cursor walk should have surfaced every rule_id exactly once; got %d distinct over %d pages", len(seen), pages)
	for id, count := range seen {
		require.Equal(t, 1, count, "duplicate rule_id %q across pages", id)
	}
}

func TestPolicyDecisionsHandlerRejectsInvalidParams(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))

	tests := []struct {
		name string
		path string
	}{
		{"limit_above_max", "/api/v1/policy/decisions?tenant=default&limit=999"},
		{"limit_negative", "/api/v1/policy/decisions?tenant=default&limit=-1"},
		{"limit_non_numeric", "/api/v1/policy/decisions?tenant=default&limit=many"},
		{"unknown_source", "/api/v1/policy/decisions?tenant=default&source=satellite"},
		{"unknown_type", "/api/v1/policy/decisions?tenant=default&type=apocalypse"},
		{"bad_since", "/api/v1/policy/decisions?tenant=default&since=yesterday"},
		{"bad_cursor", "/api/v1/policy/decisions?tenant=default&cursor=NOT-BASE64-PAYLOAD!!"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := adminCtx(httptest.NewRequest(http.MethodGet, tc.path, nil))
			r := httptest.NewRecorder()
			mux.ServeHTTP(r, req)
			require.Equal(t, http.StatusBadRequest, r.Code, r.Body.String())
		})
	}
}

func TestPolicyDecisionsHandlerReturns503WhenStoreUnavailable(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	s.decisionLogStore = nil
	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/policy/decisions?tenant=default", nil))
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, req)
	require.Equal(t, http.StatusServiceUnavailable, r.Code, r.Body.String())
}

func TestPolicyDecisionsHandlerRequiresPolicyReadPermission(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))

	// No enableTestAuth: permission check uses the real stack and rejects an
	// unauthenticated request with 401/403.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/decisions?tenant=default", nil)
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, req)
	require.True(t, r.Code == http.StatusUnauthorized || r.Code == http.StatusForbidden,
		"expected 401/403 for unauthenticated request, got %d: %s", r.Code, r.Body.String())
}

// unifiedDecisionRecordFixture builds a DecisionLogRecord populated for the
// extended schema introduced by Backend 5b. The legacy Verdict field is
// derived from the unified DecisionType so /governance/decisions transforms
// keep working post-amendment.
func unifiedDecisionRecordFixture(tenant, ruleID string, ts int64, source policy.DecisionSource, dt policy.DecisionType) model.DecisionLogRecord {
	return model.DecisionLogRecord{
		Tenant:        tenant,
		JobID:         strings.ReplaceAll(ruleID, "rule-", "job-"),
		AgentID:       "agent-default",
		Topic:         "topic-default",
		Verdict:       safetyVerdictForDecisionType(dt),
		RuleID:        ruleID,
		PolicyVersion: "policy-v1",
		Reason:        "fixture",
		Timestamp:     ts,
		Source:        source,
		Type:          dt,
		Trace:         []policy.TraceStep{{RuleID: ruleID, DecisionType: dt, Reason: "fixture", Timestamp: time.UnixMilli(ts).UTC()}},
	}
}

// safetyVerdictForDecisionType maps the unified DecisionType onto the legacy
// SafetyDecision used by /governance/decisions. quarantine + redact have no
// SafetyDecision equivalent today; they map to SafetyAllow because the legacy
// verdict log never recorded output-policy outcomes — the unified field
// preserves the actual decision via Type.
func safetyVerdictForDecisionType(dt policy.DecisionType) model.SafetyDecision {
	switch dt {
	case policy.DecisionAllow, policy.DecisionQuarantine, policy.DecisionRedact:
		return model.SafetyAllow
	case policy.DecisionDeny:
		return model.SafetyDeny
	case policy.DecisionRequireHuman:
		return model.SafetyRequireApproval
	case policy.DecisionThrottle:
		return model.SafetyThrottle
	case policy.DecisionAllowWithConstraints:
		return model.SafetyAllowWithConstraints
	default:
		return model.SafetyAllow
	}
}
