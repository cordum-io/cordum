package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// EDGE-067 — lifecycle endpoints (heartbeat, end-session, end-execution,
// list-sessions, list-executions) cross-tenant isolation.

func TestEdgeCrossTenantHeartbeatRejectsForeignSession(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/sessions/"+fix.tenantB.session.SessionID+"/heartbeat", `{}`)

	assertCrossTenantBlocked(t, rr, "heartbeat foreign session")
	fix.assertNoTenantBLeak(t, rr, "heartbeat foreign session")
}

func TestEdgeCrossTenantEndSessionRejectsForeignSession(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/sessions/"+fix.tenantB.session.SessionID+"/end", `{"status":"ended"}`)

	assertCrossTenantBlocked(t, rr, "end foreign session")
	fix.assertNoTenantBLeak(t, rr, "end foreign session")
}

func TestEdgeCrossTenantEndExecutionRejectsForeignExecution(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/executions/"+fix.tenantB.execution.ExecutionID+"/end", `{"status":"succeeded"}`)

	assertCrossTenantBlocked(t, rr, "end foreign execution")
	fix.assertNoTenantBLeak(t, rr, "end foreign execution")
}

func TestEdgeCrossTenantListSessionsReturnsOnlyOwnTenant(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodGet, "/api/v1/edge/sessions", "")

	if rr.Code != http.StatusOK {
		t.Fatalf("list sessions as A status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// Tenant A should see exactly its own session and never tenant B's.
	if !strings.Contains(body, fix.tenantA.session.SessionID) {
		t.Fatalf("list sessions for A missing A's own session_id %q: %s", fix.tenantA.session.SessionID, body)
	}
	if strings.Contains(body, fix.tenantB.session.SessionID) {
		t.Fatalf("list sessions for A leaked B's session_id %q: %s", fix.tenantB.session.SessionID, body)
	}
	if strings.Contains(body, fix.tenantB.tenantID) {
		t.Fatalf("list sessions for A leaked B's tenant_id %q: %s", fix.tenantB.tenantID, body)
	}
}

func TestEdgeCrossTenantListExecutionsBySessionIsScoped(t *testing.T) {
	fix := newCrossTenantFixture(t)

	// /api/v1/edge/executions requires a session_id (or job_id/trace_id) query
	// filter — see RedisStore.ListExecutions. Probe with tenantB's session_id
	// while authenticated as A. Even though B's session has executions,
	// A's tenant-scoped query yields zero items (the index is per-tenant).
	rr := fix.asAttacker(t, http.MethodGet, "/api/v1/edge/executions?session_id="+fix.tenantB.session.SessionID, "")

	if rr.Code != http.StatusOK {
		t.Fatalf("list executions A asking for B's session_id status = %d, want 200 (empty page); body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), fix.tenantB.execution.ExecutionID) {
		t.Fatalf("list executions A leaked B's execution_id %q: %s", fix.tenantB.execution.ExecutionID, rr.Body.String())
	}

	// Sanity: A's own session_id returns A's execution.
	rrOwn := fix.asAttacker(t, http.MethodGet, "/api/v1/edge/executions?session_id="+fix.tenantA.session.SessionID, "")
	if rrOwn.Code != http.StatusOK {
		t.Fatalf("list executions A asking for A's own session_id status = %d, want 200; body=%s", rrOwn.Code, rrOwn.Body.String())
	}
	if !strings.Contains(rrOwn.Body.String(), fix.tenantA.execution.ExecutionID) {
		t.Fatalf("list executions for A missing A's own execution_id %q: %s", fix.tenantA.execution.ExecutionID, rrOwn.Body.String())
	}
}
