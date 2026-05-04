package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

// EDGE-060 reopen #1 — idempotency tests for handleCreateEdgeExecution.
// Mirrors handleCreateEdgeSession idempotency contract: same key + same
// body returns the SAME execution_id (cached replay); same key +
// different body returns 409 idempotency_conflict; missing key falls
// back to today's non-idempotent flow.

const executionIdempotencyBody = `{
    "session_id":"%s",
    "adapter":"claude-code-hook",
    "mode":"local-dev",
    "policy_snapshot":"snap-execution-idempotency-test",
    "trace_id":"trace-execution-idempotency"
}`

// createSessionForExecutionIdempotency creates a session via the test
// scaffolding so the execution-create can attach to a real parent.
// Reuses createEdgeRouteSession from edge_routes_test.go.
func createSessionForExecutionIdempotency(t *testing.T, handler http.Handler) edgeSessionCreateResponseJSON {
	t.Helper()
	return createEdgeRouteSession(t, handler)
}

func executionIdempotencyBodyFor(sessionID string) string {
	return strings.Replace(executionIdempotencyBody, "%s", sessionID, 1)
}

// TestCreateEdgeExecutionIdempotencyReplay pins the EDGE-060 contract
// for handleCreateEdgeExecution: same key + same body returns the SAME
// execution_id. Without idempotency, agentd retry of an execution-
// create burns against the per-session execution cap and pollutes the
// dashboard timeline with ghost executions.
func TestCreateEdgeExecutionIdempotencyReplay(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	session := createSessionForExecutionIdempotency(t, handler)
	const idempotencyKey = "edge-060-exec-replay-key"

	body := executionIdempotencyBodyFor(session.SessionID)
	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/executions", body, idempotencyKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	var firstResp edgecore.AgentExecution
	if err := json.Unmarshal(first.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("decode first response: %v body=%s", err, first.Body.String())
	}
	if firstResp.ExecutionID == "" {
		t.Fatalf("first response missing execution_id; body=%s", first.Body.String())
	}

	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/executions", body, idempotencyKey)
	if second.Code != http.StatusCreated {
		t.Fatalf("second POST = %d, want 201 (cached replay) body=%s", second.Code, second.Body.String())
	}
	var secondResp edgecore.AgentExecution
	if err := json.Unmarshal(second.Body.Bytes(), &secondResp); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if secondResp.ExecutionID != firstResp.ExecutionID {
		t.Fatalf("idempotent retry produced different execution_id: first=%q second=%q",
			firstResp.ExecutionID, secondResp.ExecutionID)
	}
}

// TestCreateEdgeExecutionIdempotencyConflictOnBodyChange — same key +
// different body returns 409 idempotency_conflict.
func TestCreateEdgeExecutionIdempotencyConflictOnBodyChange(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	session := createSessionForExecutionIdempotency(t, handler)
	const idempotencyKey = "edge-060-exec-conflict-key"

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/executions",
		executionIdempotencyBodyFor(session.SessionID), idempotencyKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201 body=%s", first.Code, first.Body.String())
	}

	differentBody := strings.Replace(executionIdempotencyBodyFor(session.SessionID),
		"snap-execution-idempotency-test", "DIFFERENT-snap", 1)
	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/executions",
		differentBody, idempotencyKey)
	if second.Code != http.StatusConflict {
		t.Fatalf("conflict POST = %d, want 409 body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "idempotency") {
		t.Fatalf("expected idempotency-shaped error body; got %s", second.Body.String())
	}
}

// TestCreateEdgeExecutionMissingKeyNonIdempotent — backward compat: no
// header → fresh execution_id each call.
func TestCreateEdgeExecutionMissingKeyNonIdempotent(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	session := createSessionForExecutionIdempotency(t, handler)

	body := executionIdempotencyBodyFor(session.SessionID)
	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/executions", body, "")
	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/executions", body, "")
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("expected both POSTs to be 201; got first=%d second=%d", first.Code, second.Code)
	}
	var firstResp, secondResp edgecore.AgentExecution
	if err := json.Unmarshal(first.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if firstResp.ExecutionID == secondResp.ExecutionID {
		t.Fatalf("non-idempotent POSTs returned same execution_id %q — keys must produce fresh UUIDs",
			firstResp.ExecutionID)
	}
}
