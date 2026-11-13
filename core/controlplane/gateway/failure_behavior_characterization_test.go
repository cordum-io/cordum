package gateway

import (
	"errors"
	"net/http"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/stretchr/testify/require"
)

func TestFailureBehaviorEdgeEnforceKernelUnavailableDeniesKnownSafe(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{err: errors.New("safety unavailable")}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeEvaluateSessionWithPolicyMode(t, handler, edgecore.PolicyModeEnforce)
	body := `{"tenant_id":"` + edgeRouteTenant + `","principal_id":"principal-edge-a",
		"session_id":"` + session.SessionID + `","execution_id":"` + session.ExecutionID + `",
		"agent_product":"claude-code","layer":"hook","kind":"hook.pre_tool_use",
		"tool_name":"Bash","input_redacted":{"command":"npm test"}}`

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	require.Equal(t, "DENY", resp.Decision)
	require.Equal(t, "deny", resp.PermissionDecision)
	require.True(t, resp.Degraded)
	require.Equal(t, "safety_unavailable", resp.ErrorCode)
	events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	require.Len(t, events, 1)
	require.NotEmpty(t, resp.EventID)
	require.Equal(t, resp.EventID, events[0].EventID)
	require.Equal(t, edgecore.DecisionDeny, events[0].Decision)
	require.Equal(t, edgecore.EventKindPolicyDegraded, events[0].Kind)

	// The same request and session produce a persisted ALLOW after recovery.
	safety.mu.Lock()
	safety.err = nil
	safety.mu.Unlock()
	rr = edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	resp = edgeEvaluateResponseJSON{}
	decodeEdgeRouteJSON(t, rr, &resp)
	require.Equal(t, "ALLOW", resp.Decision)
	require.Equal(t, "allow", resp.PermissionDecision)
	require.False(t, resp.Degraded)
	events = listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	require.Len(t, events, 2)
	assertFailureBehaviorRecoveryEvent(t, events, resp.EventID)
}

func assertFailureBehaviorRecoveryEvent(t *testing.T, events []edgecore.AgentActionEvent, eventID string) {
	t.Helper()
	for _, event := range events {
		if event.EventID == eventID {
			require.Equal(t, edgecore.DecisionAllow, event.Decision)
			return
		}
	}
	t.Fatal("recovery response has no matching persisted event")
}
