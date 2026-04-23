package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/governance"
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
