package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// EDGE-067 — POST /api/v1/edge/evaluate cross-tenant isolation.

func TestEdgeCrossTenantEvaluateRejectsForeignSessionAndExecution(t *testing.T) {
	fix := newCrossTenantFixture(t)

	// Auth as tenantA, header A, but session+execution IDs belong to tenantB.
	// store.GetSession(ctx, tenantA, sessionB) returns not-found → 404.
	body := edgeEvaluateBody(fix.tenantB.session.SessionID, fix.tenantB.execution.ExecutionID, fix.tenantA.tenantID, "Bash", map[string]any{"command": "npm test"})
	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/evaluate", body)

	assertCrossTenantBlocked(t, rr, "evaluate with foreign session+execution")
	fix.assertNoTenantBLeak(t, rr, "evaluate with foreign session+execution")
	if rr.Code != http.StatusNotFound {
		t.Logf("note: status %d != 404 preferred; got %d", rr.Code, rr.Code)
	}
}

func TestEdgeCrossTenantEvaluateRejectsBodyTenantInjection(t *testing.T) {
	fix := newCrossTenantFixture(t)

	// Body advertises tenant_id=B but header is A. edgeTenantFromRequest
	// returns 403 tenant_mismatch.
	body := edgeEvaluateBody(fix.tenantA.session.SessionID, fix.tenantA.execution.ExecutionID, fix.tenantB.tenantID, "Bash", map[string]any{"command": "npm test"})
	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/evaluate", body)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("body tenant_id=B header=A evaluate status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), edgeErrCodeTenantMismatch) {
		t.Fatalf("body tenant injection on evaluate missing %q: %s", edgeErrCodeTenantMismatch, rr.Body.String())
	}
}

func TestEdgeCrossTenantEvaluateRejectsHeaderInjection(t *testing.T) {
	fix := newCrossTenantFixture(t)

	// Auth=A, header=B, body session/execution belong to B. The gateway
	// must reject the auth-vs-header mismatch BEFORE looking up the
	// resource — otherwise an attacker could probe B's resource existence
	// via response shape.
	body := edgeEvaluateBody(fix.tenantB.session.SessionID, fix.tenantB.execution.ExecutionID, "", "Bash", map[string]any{"command": "npm test"})
	rr := fix.asAttackerWithHeader(t, http.MethodPost, "/api/v1/edge/evaluate", body, fix.tenantB.tenantID)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("auth=A header=B evaluate status = %d, want 403 (header injection); body=%s", rr.Code, rr.Body.String())
	}
}
