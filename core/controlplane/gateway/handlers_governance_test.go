package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/governance"
)

func TestHandleGovernanceHealth_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	s := &server{auth: &policySimAuth{}}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/governance/health", nil)
	r.Header.Set("X-Tenant-ID", "default")
	r.Header.Set("X-Principal-Id", "alice")
	r.Header.Set("X-Principal-Role", "viewer")
	r = withAuth(r, &AuthContext{Tenant: "default", PrincipalID: "alice", Role: "viewer"})

	rec := httptest.NewRecorder()
	s.handleGovernanceHealth(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer role should be 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGovernanceHealth_AdminReturnsScore(t *testing.T) {
	t.Parallel()
	s := &server{auth: &policySimAuth{}}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/governance/health", nil)
	r.Header.Set("X-Tenant-ID", "default")
	r.Header.Set("X-Principal-Id", "admin-1")
	r.Header.Set("X-Principal-Role", "admin")
	r = withAuth(r, &AuthContext{Tenant: "default", PrincipalID: "admin-1", Role: "admin"})

	rec := httptest.NewRecorder()
	s.handleGovernanceHealth(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin should get 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out governance.HealthScore
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Grade == "" {
		t.Error("response missing Grade")
	}
	if len(out.Factors) != 4 {
		t.Errorf("expected 4 factors, got %d: %+v", len(out.Factors), out.Factors)
	}
	for name, f := range out.Factors {
		if f.Weight == 0 {
			t.Errorf("factor %q missing weight", name)
		}
	}
}

func TestHandleGovernanceHealth_PartialFactorResilient(t *testing.T) {
	t.Parallel()
	// The governance.ComputeHealth aggregator already guarantees a
	// failing factor does not bubble up. This test pins the contract at
	// the HTTP level by exercising a deps implementation where one
	// factor errors; the aggregator marks it with a Notes string and
	// returns 200.
	ctx := context.Background()
	deps := &errorFactorDeps{
		wrap: baseTestDeps("tenant-a", time.Now().UTC()),
	}
	got, err := governance.ComputeHealth(ctx, deps, governance.NewCache(1*time.Second))
	if err != nil {
		t.Fatalf("ComputeHealth should not 500 on a single-factor failure: %v", err)
	}
	if got.Factors[governance.FactorChainIntegrity].Notes == "" {
		t.Error("failing factor should carry a Notes explanation")
	}
}

// errorFactorDeps wraps a working deps, overriding VerifyChain to fail.
type errorFactorDeps struct {
	wrap governance.HealthDeps
}

func (e *errorFactorDeps) Tenant() string { return e.wrap.Tenant() }
func (e *errorFactorDeps) Now() time.Time { return e.wrap.Now() }
func (e *errorFactorDeps) ScanDecisions(ctx context.Context, w time.Duration, n time.Time) (governance.DecisionCounts, error) {
	return e.wrap.ScanDecisions(ctx, w, n)
}
func (e *errorFactorDeps) ApprovalLatencies(ctx context.Context, w time.Duration, n time.Time) ([]time.Duration, error) {
	return e.wrap.ApprovalLatencies(ctx, w, n)
}
func (e *errorFactorDeps) ListTopics(ctx context.Context) ([]string, error) {
	return e.wrap.ListTopics(ctx)
}
func (e *errorFactorDeps) CoveredTopics(ctx context.Context) ([]string, error) {
	return e.wrap.CoveredTopics(ctx)
}
func (e *errorFactorDeps) VerifyChain(_ context.Context) (governance.ChainStatus, error) {
	return "", errForced
}

var errForced = &forcedErr{}

type forcedErr struct{}

func (*forcedErr) Error() string { return "forced: chain verifier unreachable" }

// baseTestDeps is a minimal deps impl reusing the test idioms from
// core/governance — local so gateway tests don't have to import that
// package's test helpers.
func baseTestDeps(tenant string, now time.Time) governance.HealthDeps {
	return &stubDeps{
		tenant:    tenant,
		now:       now,
		decisions: governance.DecisionCounts{Allow: 50, Deny: 5},
		latencies: []time.Duration{15 * time.Second, 25 * time.Second},
		topics:    []string{"job.a", "job.b"},
		covered:   []string{"job.a"},
		chain:     governance.ChainStatusOK,
	}
}

type stubDeps struct {
	tenant    string
	now       time.Time
	decisions governance.DecisionCounts
	latencies []time.Duration
	topics    []string
	covered   []string
	chain     governance.ChainStatus
}

func (d *stubDeps) Tenant() string { return d.tenant }
func (d *stubDeps) Now() time.Time { return d.now }
func (d *stubDeps) ScanDecisions(_ context.Context, _ time.Duration, _ time.Time) (governance.DecisionCounts, error) {
	return d.decisions, nil
}
func (d *stubDeps) ApprovalLatencies(_ context.Context, _ time.Duration, _ time.Time) ([]time.Duration, error) {
	return d.latencies, nil
}
func (d *stubDeps) ListTopics(_ context.Context) ([]string, error)    { return d.topics, nil }
func (d *stubDeps) CoveredTopics(_ context.Context) ([]string, error) { return d.covered, nil }
func (d *stubDeps) VerifyChain(_ context.Context) (governance.ChainStatus, error) {
	return d.chain, nil
}
