package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// EDGE-060 reopen #1 — cross-tenant isolation tests per DoD #4.
// Asserts that the SAME Idempotency-Key reused by tenant-A and tenant-B
// hashes to DIFFERENT records (because the hash payload includes
// tenant_id after auth-context override). Both calls must succeed
// independently — no cross-tenant leakage of cached responses.
//
// The trust boundary: prepareEdgeIdempotencyRequest hashes after
// tenant override per the EDGE-008.7 invariant. A malicious tenant-B
// client cannot replay tenant-A's cached response by reusing
// tenant-A's idempotency key — the tenant override produces a
// different hash entirely.

// TestCreateEdgeSessionCrossTenantIsolation pins DoD #4: the same
// Idempotency-Key used by two different tenants produces two
// independent sessions. Demonstrates the tenant-scoped hash boundary.
func TestCreateEdgeSessionCrossTenantIsolation(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	const sharedKey = "edge-060-cross-tenant-shared-key"

	tenantA := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, edgeRouteTestAPIKey, edgeRouteTenant,
		"/api/v1/edge/sessions", sessionIdempotencyBody, sharedKey)
	if tenantA.Code != http.StatusCreated {
		t.Fatalf("tenant-A POST = %d, want 201 body=%s", tenantA.Code, tenantA.Body.String())
	}

	tenantB := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, edgeRouteOtherAPIKey, edgeRouteOtherTenant,
		"/api/v1/edge/sessions", sessionIdempotencyBody, sharedKey)
	if tenantB.Code != http.StatusCreated {
		t.Fatalf("tenant-B POST = %d, want 201 (NOT cached replay of tenant-A); body=%s", tenantB.Code, tenantB.Body.String())
	}

	var aResp, bResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, tenantA, &aResp)
	decodeEdgeRouteJSON(t, tenantB, &bResp)
	if aResp.SessionID == bResp.SessionID {
		t.Fatalf("cross-tenant idempotent reuse leaked session_id across tenant boundary: A=%q B=%q",
			aResp.SessionID, bResp.SessionID)
	}
	if aResp.ExecutionID == bResp.ExecutionID {
		t.Fatalf("cross-tenant idempotent reuse leaked execution_id: A=%q B=%q",
			aResp.ExecutionID, bResp.ExecutionID)
	}
}

// TestCreateEdgeExecutionCrossTenantIsolation pins DoD #4 for the
// execution-create endpoint. Each tenant has its own session and
// independent idempotency hash space.
func TestCreateEdgeExecutionCrossTenantIsolation(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	const sharedKey = "edge-060-exec-cross-tenant-shared-key"

	// Each tenant creates its own session.
	sessionA := createEdgeRouteSession(t, handler)
	sessionB := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, edgeRouteOtherAPIKey, edgeRouteOtherTenant,
		"/api/v1/edge/sessions", sessionIdempotencyBody, "")
	if sessionB.Code != http.StatusCreated {
		t.Fatalf("create tenant-B session = %d, body=%s", sessionB.Code, sessionB.Body.String())
	}
	var sessionBResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, sessionB, &sessionBResp)

	// Tenant-A creates an execution under their session with shared key.
	execA := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, edgeRouteTestAPIKey, edgeRouteTenant,
		"/api/v1/edge/executions",
		executionIdempotencyBodyFor(sessionA.SessionID), sharedKey)
	if execA.Code != http.StatusCreated {
		t.Fatalf("tenant-A exec POST = %d body=%s", execA.Code, execA.Body.String())
	}

	// Tenant-B uses the SAME idempotency key on their own session —
	// must not replay tenant-A's cached response.
	execB := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, edgeRouteOtherAPIKey, edgeRouteOtherTenant,
		"/api/v1/edge/executions",
		executionIdempotencyBodyFor(sessionBResp.SessionID), sharedKey)
	if execB.Code != http.StatusCreated {
		t.Fatalf("tenant-B exec POST = %d, want 201 (NOT cached replay of tenant-A); body=%s",
			execB.Code, execB.Body.String())
	}

	// Both responses must be DISTINCT executions. The hash boundary is
	// tenant-scoped via prepareEdgeIdempotencyRequest's tenant-override.
	if execA.Body.String() == execB.Body.String() {
		t.Fatalf("cross-tenant idempotent reuse leaked execution body across tenant boundary")
	}
}

// TestApproveEdgeApprovalCrossTenantIsolation pins DoD #4 for the
// approve endpoint. Each tenant's approval is in a separate hash
// namespace; a cross-tenant key reuse must not replay another tenant's
// resolution.
func TestApproveEdgeApprovalCrossTenantIsolation(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	// Seed approvals with requesters DIFFERENT from each tenant's API-key
	// principal so the admin role can resolve without tripping the
	// self-approval guard.
	approvalA := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a-requester", "approve-cross-tenant-A")
	approvalB := seedGatewayEdgeApproval(t, s, edgeRouteOtherTenant, "principal-edge-b-requester", "approve-cross-tenant-B")
	const sharedKey = "edge-060-approve-cross-tenant-shared-key"

	respA := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approvalA.ApprovalRef+"/approve",
		approvalIdempotencyBody, sharedKey)
	if respA.Code != http.StatusOK {
		t.Fatalf("tenant-A approve = %d, want 200 body=%s", respA.Code, respA.Body.String())
	}

	// Tenant-B approves their own approval with the SAME idempotency
	// key. Must NOT replay tenant-A's cached body.
	reqB := httptest.NewRequest(http.MethodPost,
		"/api/v1/edge/approvals/"+approvalB.ApprovalRef+"/approve",
		strings.NewReader(approvalIdempotencyBody))
	addEdgeRouteAuthFor(reqB, edgeRouteOtherAPIKey)
	reqB.Header.Set("X-Tenant-ID", edgeRouteOtherTenant)
	reqB.Header.Set("Content-Type", "application/json")
	reqB.Header.Set("Idempotency-Key", sharedKey)
	rrB := httptest.NewRecorder()
	handler.ServeHTTP(rrB, reqB)
	if rrB.Code != http.StatusOK {
		t.Fatalf("tenant-B approve = %d, want 200 (NOT cached replay of tenant-A); body=%s",
			rrB.Code, rrB.Body.String())
	}
	if rrB.Body.String() == respA.Body.String() {
		t.Fatalf("cross-tenant idempotent reuse leaked approval body across tenant boundary")
	}
	// Tenant-B's response should reference tenant-B's approval ref.
	if !strings.Contains(rrB.Body.String(), approvalB.ApprovalRef) {
		t.Fatalf("tenant-B approve body missing tenant-B approval_ref %q; got %s",
			approvalB.ApprovalRef, rrB.Body.String())
	}
}
