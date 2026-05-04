package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// EDGE-060 step 3 — idempotency tests for handleCreateEdgeSession.

const sessionIdempotencyBody = `{
    "agent_product":"claude-code",
    "agent_version":"1.2.3",
    "mode":"local-dev",
    "policy_snapshot":"snap-idempotency-test",
    "policy_mode":"observe"
}`

// edgeRoutePOSTWithIdempotencyKey is defined in edge_events_idempotency_test.go;
// reused here.

// TestCreateEdgeSessionIdempotencyReplay proves the EDGE-060 contract:
// two POSTs with the same Idempotency-Key + identical body produce the
// SAME response (same session_id, same execution_id, same trace_id).
// Without idempotency, each POST would generate fresh UUIDs — the
// agentd retry path would create ghost sessions in the dashboard.
func TestCreateEdgeSessionIdempotencyReplay(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	const idempotencyKey = "edge-060-test-replay-key"

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, idempotencyKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	var firstResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, first, &firstResp)
	if firstResp.SessionID == "" {
		t.Fatalf("first POST missing session_id; body=%s", first.Body.String())
	}

	// Second POST with same key + same body — must replay.
	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, idempotencyKey)
	if second.Code != http.StatusCreated {
		t.Fatalf("second POST = %d, want 201 (cached replay) body=%s", second.Code, second.Body.String())
	}
	var secondResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, second, &secondResp)
	if secondResp.SessionID != firstResp.SessionID {
		t.Fatalf("idempotent retry produced different session_id: first=%q second=%q",
			firstResp.SessionID, secondResp.SessionID)
	}
	if secondResp.ExecutionID != firstResp.ExecutionID {
		t.Fatalf("idempotent retry produced different execution_id: first=%q second=%q",
			firstResp.ExecutionID, secondResp.ExecutionID)
	}
	if secondResp.TraceID != firstResp.TraceID {
		t.Fatalf("idempotent retry produced different trace_id: first=%q second=%q",
			firstResp.TraceID, secondResp.TraceID)
	}
}

// TestCreateEdgeSessionIdempotencyConflictOnBodyChange proves that
// reusing an Idempotency-Key with a DIFFERENT body returns 409 — the
// hash mismatch is detected and the second request is refused, so a
// buggy client can't accidentally pivot a cached response onto a
// different intent.
func TestCreateEdgeSessionIdempotencyConflictOnBodyChange(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	const idempotencyKey = "edge-060-test-conflict-key"

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, idempotencyKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201 body=%s", first.Code, first.Body.String())
	}

	// Same key, different body — 409 idempotency_conflict.
	differentBody := `{
        "agent_product":"claude-code",
        "agent_version":"9.9.9",
        "mode":"local-dev",
        "policy_snapshot":"DIFFERENT-snap",
        "policy_mode":"enforce"
    }`
	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", differentBody, idempotencyKey)
	if second.Code != http.StatusConflict {
		t.Fatalf("second POST with different body = %d, want 409 body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "idempotency") {
		t.Fatalf("expected idempotency-shaped error body; got %s", second.Body.String())
	}
}

// TestCreateEdgeSessionMissingKeyNonIdempotent proves that a POST
// WITHOUT an Idempotency-Key header still works as today — every call
// generates a fresh session. Backward compat with clients that haven't
// adopted the idempotency contract yet.
func TestCreateEdgeSessionMissingKeyNonIdempotent(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, "")
	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, "")
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("expected both POSTs to be 201; got first=%d second=%d", first.Code, second.Code)
	}
	var firstResp, secondResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, first, &firstResp)
	decodeEdgeRouteJSON(t, second, &secondResp)
	if firstResp.SessionID == secondResp.SessionID {
		t.Fatalf("non-idempotent POSTs returned same session_id %q — keys must produce fresh UUIDs", firstResp.SessionID)
	}
	if firstResp.ExecutionID == secondResp.ExecutionID {
		t.Fatalf("non-idempotent POSTs returned same execution_id — keys must produce fresh UUIDs")
	}
}
