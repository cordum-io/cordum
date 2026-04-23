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
	"github.com/cordum/cordum/core/governance"
	"github.com/cordum/cordum/core/model"
)

func TestGovernanceHealthRouteRegistered(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/governance/health?tenant=default", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var res governance.HealthScore
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Grade == "" {
		t.Fatal("grade missing from response")
	}
	if len(res.Factors) != 4 {
		t.Fatalf("factors = %d, want 4 (%+v)", len(res.Factors), res.Factors)
	}
	for _, key := range []string{
		governance.FactorDenialRate,
		governance.FactorApprovalLatencyP95,
		governance.FactorPolicyCoverage,
		governance.FactorChainIntegrity,
	} {
		factor, ok := res.Factors[key]
		if !ok {
			t.Fatalf("missing factor %q in %+v", key, res.Factors)
		}
		if factor.Weight == 0 {
			t.Fatalf("factor %q weight = 0", key)
		}
	}
}

func TestGovernanceHealthRequiresAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/governance/health?tenant=default", nil), &auth.AuthContext{
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

func TestGovernanceHealthApprovalLatenciesSkipsPendingRecords(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	now := time.Now().UTC()
	jobID := "approval-pending"

	if err := s.decisionLogStore.AppendDecision(ctx, model.DecisionLogRecord{
		JobID:     jobID,
		Tenant:    "default",
		Verdict:   model.SafetyRequireApproval,
		Timestamp: now.Add(-time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatalf("append decision: %v", err)
	}

	samples, err := newGovernanceHealthDeps(s, "default").ApprovalLatencies(ctx, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("ApprovalLatencies returned error for pending/missing approval record: %v", err)
	}
	if len(samples) != 0 {
		t.Fatalf("samples = %d, want 0 for pending/missing approval record", len(samples))
	}
}

func TestGovernanceHealthApprovalLatencyLookupErrorMarksUnavailable(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	now := time.Now().UTC()
	jobID := "approval-lookup-fails"

	if err := s.decisionLogStore.AppendDecision(ctx, model.DecisionLogRecord{
		JobID:     jobID,
		Tenant:    "default",
		Verdict:   model.SafetyRequireApproval,
		Timestamp: now.Add(-time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatalf("append decision: %v", err)
	}

	if err := s.jobStore.Client().Close(); err != nil {
		t.Fatalf("close job store client: %v", err)
	}

	score, err := governance.ComputeHealth(ctx, newGovernanceHealthDeps(s, "default"), nil)
	if err != nil {
		t.Fatalf("ComputeHealth: %v", err)
	}
	factor := score.Factors[governance.FactorApprovalLatencyP95]
	if factor.Score != governance.NeutralFactorScore {
		t.Fatalf("approval latency score = %d, want neutral %d", factor.Score, governance.NeutralFactorScore)
	}
	if !strings.Contains(factor.Notes, "unavailable:") || !strings.Contains(factor.Notes, "approval latency lookup "+jobID) {
		t.Fatalf("approval latency notes = %q, want unavailable lookup error", factor.Notes)
	}
}
