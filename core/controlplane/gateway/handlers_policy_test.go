package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPolicyEvaluate_UserRoleRejected proves red-team #19 is closed:
// a non-admin (user role) caller cannot access policy/evaluate.
func TestPolicyEvaluate_UserRoleRejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)

	body := `{"topic": "job.default", "risk_tags": ["low"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "test-api-key")
	req = withAuth(req, &AuthContext{Tenant: "default", Role: "user", PrincipalID: "attacker"})
	rec := httptest.NewRecorder()

	s.handlePolicyEvaluate(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("RED-TEAM #19 BYPASS: user-role caller got %d (want 403): %s", rec.Code, rec.Body.String())
	}
}

// TestPolicyEvaluate_AdminAllowed_ReturnsResponse proves admin callers
// can still use the endpoint.
func TestPolicyEvaluate_AdminAllowed_ReturnsResponse(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)

	body := `{"topic": "job.default"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "test-api-key")
	req = withAuth(req, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-user"})
	rec := httptest.NewRecorder()

	s.handlePolicyEvaluate(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("admin should be allowed, got 403: %s", rec.Body.String())
	}
}
