package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// EDGE-067 — event endpoints (create, batch, list-by-session, list-by-
// execution, export) cross-tenant isolation.

func TestEdgeCrossTenantCreateEventRejectsForeignParents(t *testing.T) {
	fix := newCrossTenantFixture(t)

	body := edgeEventWriteBody(fix.tenantB.session.SessionID, fix.tenantB.execution.ExecutionID, fix.tenantA.tenantID, "evt-edge067-cross")
	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/events", body)

	assertCrossTenantBlocked(t, rr, "create event with foreign session+execution")
	fix.assertNoTenantBLeak(t, rr, "create event with foreign session+execution")
}

func TestEdgeCrossTenantCreateEventRejectsBodyTenantInjection(t *testing.T) {
	fix := newCrossTenantFixture(t)

	// Body declares tenant_id=B while header is A. edgeTenantFromRequest
	// returns 403 tenant_mismatch.
	body := edgeEventWriteBody(fix.tenantA.session.SessionID, fix.tenantA.execution.ExecutionID, fix.tenantB.tenantID, "evt-edge067-bodyB")
	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/events", body)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("event body tenant_id=B header=A status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), edgeErrCodeTenantMismatch) {
		t.Fatalf("event body tenant injection missing %q: %s", edgeErrCodeTenantMismatch, rr.Body.String())
	}
}

func TestEdgeCrossTenantCreateEventBatchRejectsForeignParents(t *testing.T) {
	fix := newCrossTenantFixture(t)

	// Batch body with tenant=A header, but the event records reference
	// tenantB's session+execution. The handler must reject because
	// store.AppendEvents(tenant=A, sessionID=B's, ...) will not find the
	// session in A's scope.
	body := `{"events":[` +
		edgeEventWriteBody(fix.tenantB.session.SessionID, fix.tenantB.execution.ExecutionID, fix.tenantA.tenantID, "evt-edge067-batch-1") +
		`]}`
	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/events/batch", body)

	assertCrossTenantBlocked(t, rr, "batch foreign-parent rejection")
	fix.assertNoTenantBLeak(t, rr, "batch foreign-parent rejection")
}

func TestEdgeCrossTenantListSessionEventsRejectsForeignSession(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodGet, "/api/v1/edge/sessions/"+fix.tenantB.session.SessionID+"/events", "")

	// Either 404 not found or 200 with an empty list (still no leak).
	if rr.Code != http.StatusNotFound && rr.Code != http.StatusOK {
		t.Fatalf("list session events foreign-session status = %d, want 404 or 200-empty; body=%s", rr.Code, rr.Body.String())
	}
	fix.assertNoTenantBLeak(t, rr, "list session events foreign-session must not leak")
}

func TestEdgeCrossTenantListExecutionEventsRejectsForeignExecution(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodGet, "/api/v1/edge/executions/"+fix.tenantB.execution.ExecutionID+"/events", "")

	if rr.Code != http.StatusNotFound && rr.Code != http.StatusOK {
		t.Fatalf("list execution events foreign-execution status = %d, want 404 or 200-empty; body=%s", rr.Code, rr.Body.String())
	}
	fix.assertNoTenantBLeak(t, rr, "list execution events foreign-execution must not leak")
}

func TestEdgeCrossTenantExportRejectsForeignSession(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/sessions/"+fix.tenantB.session.SessionID+"/export", `{}`)

	assertCrossTenantBlocked(t, rr, "export foreign session")
	fix.assertNoTenantBLeak(t, rr, "export foreign session")
}
