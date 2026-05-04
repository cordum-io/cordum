package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// EDGE-060 step 4 — idempotency tests for handleApproveEdgeApproval +
// handleRejectEdgeApproval. State-transition wrinkle: approval ALREADY
// exists in store (created by evaluate). Idempotency-Key here protects
// against double-resolution via network retry of approve/reject — same
// key + same body returns the SAME cached response (NOT 409
// "already approved").

const approvalIdempotencyBody = `{"reason":"idempotency-test approve"}`

// approvalRoutePOSTWithIdempotencyKey is a focused helper for these
// tests: builds the route POST with the optional Idempotency-Key header.
// Reuses the addEdgeRouteAuthFor convention from the existing tests.
func approvalRoutePOSTWithIdempotencyKey(t *testing.T, handler http.Handler, apiKey, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	addEdgeRouteAuthFor(req, apiKey)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// TestApproveEdgeApprovalIdempotencyReplay pins DoD #7: a same-key +
// same-body retry of an already-resolved approval returns the cached
// 200 response (REPLAY, not 409 "already approved"). Validates
// EDGE-060's idempotency contract for state transitions.
func TestApproveEdgeApprovalIdempotencyReplay(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "approve-idempotency")
	const idempotencyKey = "edge-060-approve-replay-key"

	first := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve",
		approvalIdempotencyBody, idempotencyKey)
	if first.Code != http.StatusOK {
		t.Fatalf("first approve = %d, want 200 body=%s", first.Code, first.Body.String())
	}
	firstBody := first.Body.String()

	// Second POST with same key + same body — must REPLAY cached 200,
	// NOT return 409 "already approved" (DoD #7 invariant).
	second := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve",
		approvalIdempotencyBody, idempotencyKey)
	if second.Code != http.StatusOK {
		t.Fatalf("second approve (replay) = %d, want 200 cached body=%s", second.Code, second.Body.String())
	}
	if second.Body.String() != firstBody {
		t.Fatalf("idempotent retry produced different body:\nfirst=%s\nsecond=%s", firstBody, second.Body.String())
	}
}

// TestApproveEdgeApprovalDifferentKeyOnTerminal proves a DIFFERENT
// Idempotency-Key hitting an already-terminal approval still surfaces
// the existing 409 "already approved" path.
func TestApproveEdgeApprovalDifferentKeyOnTerminal(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "approve-different-key")

	first := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve",
		approvalIdempotencyBody, "edge-060-key-A")
	if first.Code != http.StatusOK {
		t.Fatalf("first approve = %d, want 200 body=%s", first.Code, first.Body.String())
	}

	second := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve",
		approvalIdempotencyBody, "edge-060-key-B")
	if second.Code != http.StatusConflict {
		t.Fatalf("different-key second approve = %d, want 409 body=%s", second.Code, second.Body.String())
	}
}

// TestApproveEdgeApprovalIdempotencyConflictOnBodyChange proves that
// reusing an Idempotency-Key with a DIFFERENT body returns 409
// idempotency_conflict.
func TestApproveEdgeApprovalIdempotencyConflictOnBodyChange(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "approve-body-conflict")
	const idempotencyKey = "edge-060-approve-conflict-key"

	first := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve",
		approvalIdempotencyBody, idempotencyKey)
	if first.Code != http.StatusOK {
		t.Fatalf("first approve = %d, want 200 body=%s", first.Code, first.Body.String())
	}

	differentBody := `{"reason":"DIFFERENT reason injected"}`
	second := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve",
		differentBody, idempotencyKey)
	if second.Code != http.StatusConflict {
		t.Fatalf("conflict approve = %d, want 409 body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "idempotency") {
		t.Fatalf("expected idempotency-shaped error body; got %s", second.Body.String())
	}
}

// TestApproveEdgeApprovalMissingKeyNonIdempotent — no Idempotency-Key →
// today's non-idempotent path. Second POST hits the store's terminal
// state and returns 409.
func TestApproveEdgeApprovalMissingKeyNonIdempotent(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "approve-no-key")

	first := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve",
		approvalIdempotencyBody, "")
	if first.Code != http.StatusOK {
		t.Fatalf("first no-key approve = %d, want 200 body=%s", first.Code, first.Body.String())
	}
	second := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve",
		approvalIdempotencyBody, "")
	if second.Code != http.StatusConflict {
		t.Fatalf("second no-key approve = %d, want 409 (terminal) body=%s", second.Code, second.Body.String())
	}
}

// TestRejectEdgeApprovalIdempotencyReplay mirrors the approve-replay
// test for the reject endpoint.
func TestRejectEdgeApprovalIdempotencyReplay(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "reject-idempotency")
	const idempotencyKey = "edge-060-reject-replay-key"

	first := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/reject",
		`{"reason":"reject-replay-test"}`, idempotencyKey)
	if first.Code != http.StatusOK {
		t.Fatalf("first reject = %d, want 200 body=%s", first.Code, first.Body.String())
	}
	firstBody := first.Body.String()

	second := approvalRoutePOSTWithIdempotencyKey(t, handler, edgeRouteReviewerAPIKey,
		"/api/v1/edge/approvals/"+approval.ApprovalRef+"/reject",
		`{"reason":"reject-replay-test"}`, idempotencyKey)
	if second.Code != http.StatusOK {
		t.Fatalf("second reject (replay) = %d, want 200 cached body=%s", second.Code, second.Body.String())
	}
	if second.Body.String() != firstBody {
		t.Fatalf("reject idempotent retry produced different body")
	}
}
