package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// EDGE-067 — every Edge REST + WS path must prove tenant isolation. This
// file covers POST/GET session + execution endpoints. Each test is named
// TestEdgeCrossTenant* so a single `go test -run "TestEdgeCrossTenant.*"`
// runs the full sweep (DoD #5).

func TestEdgeCrossTenantCreateSessionRejectsBodyTenantInjection(t *testing.T) {
	fix := newCrossTenantFixture(t)

	body := `{"agent_product":"claude-code","agent_version":"1.2.3","mode":"local-dev","tenant_id":"` + fix.tenantB.tenantID + `"}`
	req := crossTenantRequest(t, http.MethodPost, "/api/v1/edge/sessions", body, fix.tenantA.apiKey, fix.tenantA.tenantID)
	rr := fix.do(req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("body tenant_id=B with header tenant=A status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), edgeErrCodeTenantMismatch) {
		t.Fatalf("body tenant injection error code missing %q: %s", edgeErrCodeTenantMismatch, rr.Body.String())
	}
}

func TestEdgeCrossTenantCreateSessionRejectsMissingTenantHeader(t *testing.T) {
	fix := newCrossTenantFixture(t)

	body := `{"agent_product":"claude-code","mode":"local-dev"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", strings.NewReader(body))
	addEdgeRouteAuthFor(req, fix.tenantA.apiKey)
	req.Header.Set("Content-Type", "application/json")
	rr := fix.do(req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing X-Tenant-ID status = %d, want 400 (fail closed); body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), edgeErrCodeTenantRequired) {
		t.Fatalf("missing tenant header error code missing %q: %s", edgeErrCodeTenantRequired, rr.Body.String())
	}
}

func TestEdgeCrossTenantCreateSessionRejectsTenantMismatchInHeader(t *testing.T) {
	fix := newCrossTenantFixture(t)

	body := `{"agent_product":"claude-code","mode":"local-dev"}`
	// auth=A but X-Tenant-ID=B → tenantMiddleware sees auth tenant != header
	// tenant and must reject.
	req := crossTenantRequest(t, http.MethodPost, "/api/v1/edge/sessions", body, fix.tenantA.apiKey, fix.tenantB.tenantID)
	rr := fix.do(req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("auth=A header=B status = %d, want 403 (auth-tenant != header-tenant); body=%s", rr.Code, rr.Body.String())
	}
}

func TestEdgeCrossTenantCreateExecutionRejectsForeignSession(t *testing.T) {
	fix := newCrossTenantFixture(t)

	// Auth as tenantA, header A, but session_id belongs to tenantB. Even
	// though the JSON body is well-formed and the session exists in B's
	// scope, the store lookup is keyed on tenant=A and must return 404.
	body := `{"session_id":"` + fix.tenantB.session.SessionID + `","adapter":"claude-code-hook","mode":"local-dev"}`
	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/executions", body)

	assertCrossTenantBlocked(t, rr, "create execution against foreign session")
	fix.assertNoTenantBLeak(t, rr, "create execution against foreign session")
}

func TestEdgeCrossTenantGetSessionRejectsByIDGuess(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodGet, "/api/v1/edge/sessions/"+fix.tenantB.session.SessionID, "")

	assertCrossTenantBlocked(t, rr, "get session by ID guess")
	fix.assertNoTenantBLeak(t, rr, "get session by ID guess")
	// 404 preferred — no info leak. 403 acceptable but should not echo IDs.
	if rr.Code != http.StatusNotFound {
		t.Logf("note: status %d != 404 preferred; current handler chose %d (acceptable per task rail if no leak)", rr.Code, rr.Code)
	}
}

func TestEdgeCrossTenantGetSessionRejectsHeaderInjection(t *testing.T) {
	fix := newCrossTenantFixture(t)

	// Auth=A but X-Tenant-ID=B → tenantMiddleware must reject; the gateway
	// must NOT trust the header to upgrade the request to tenant B's scope.
	rr := fix.asAttackerWithHeader(t, http.MethodGet, "/api/v1/edge/sessions/"+fix.tenantB.session.SessionID, "", fix.tenantB.tenantID)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("auth=A header=B for get session status = %d, want 403 (header injection); body=%s", rr.Code, rr.Body.String())
	}
}

func TestEdgeCrossTenantGetExecutionRejectsByIDGuess(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodGet, "/api/v1/edge/executions/"+fix.tenantB.execution.ExecutionID, "")

	assertCrossTenantBlocked(t, rr, "get execution by ID guess")
	fix.assertNoTenantBLeak(t, rr, "get execution by ID guess")
}

func TestEdgeCrossTenantGetExecutionRejectsHeaderInjection(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttackerWithHeader(t, http.MethodGet, "/api/v1/edge/executions/"+fix.tenantB.execution.ExecutionID, "", fix.tenantB.tenantID)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("auth=A header=B for get execution status = %d, want 403 (header injection); body=%s", rr.Code, rr.Body.String())
	}
}
