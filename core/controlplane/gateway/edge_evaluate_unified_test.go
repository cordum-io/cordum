package gateway

import (
	"net/http"
	"testing"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestGatewayEdgeEvaluateDualEmitsLegacyAndUnifiedDecision(t *testing.T) {
	t.Setenv(audit.EnvUnifiedDecisionMode, string(audit.UnifiedDecisionModeDual))

	stub := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_DENY,
		Reason:         "destructive shell",
		RuleId:         "edge.deny.shell",
		PolicySnapshot: "bundle-v4",
	}}
	s, handler := newEdgeEvaluateTestServer(t, stub)
	sink := &testAuditSender{}
	s.auditExporter = sink
	session := createEdgeRouteSession(t, handler)
	before := sink.Len()

	rec := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(
		session.SessionID,
		session.ExecutionID,
		edgeRouteTenant,
		"Bash",
		map[string]any{"command": "rm -rf /tmp/demo"},
	))

	if rec.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := sink.Len() - before; got != 2 {
		t.Fatalf("audit events emitted = %d, want legacy edge + policy.decision.v2", got)
	}
	legacy := sink.Get(before)
	unified := sink.Get(before + 1)
	if legacy.EventType != audit.EventEdgeActionDenied {
		t.Fatalf("legacy event type = %q, want %q", legacy.EventType, audit.EventEdgeActionDenied)
	}
	if unified.EventType != audit.EventPolicyDecisionV2 {
		t.Fatalf("unified event type = %q, want %q", unified.EventType, audit.EventPolicyDecisionV2)
	}
	if unified.Extra["source"] != "edge" || unified.Decision != "deny" || unified.MatchedRule != "edge.deny.shell" {
		t.Fatalf("unified decision = type:%q source:%q decision:%q rule:%q extra:%#v",
			unified.EventType, unified.Extra["source"], unified.Decision, unified.MatchedRule, unified.Extra)
	}
}

func TestGatewayEdgeEventWriteDualEmitsAgentdDecisionEvidence(t *testing.T) {
	t.Setenv(audit.EnvUnifiedDecisionMode, string(audit.UnifiedDecisionModeDual))

	s, handler := newEdgeRouteTestServer(t)
	sink := &testAuditSender{}
	s.auditExporter = sink
	session := createEdgeRouteSession(t, handler)
	before := sink.Len()

	rec := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-agentd-unified-audit",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-09T14:30:00Z",
		"layer":"hook",
		"kind":"hook.policy_decision",
		"action_name":"bash.exec",
		"capability":"exec.shell",
		"risk_tags":["exec"],
		"input_hash":"sha256:input-agentd",
		"decision":"DENY",
		"decision_reason":"agentd degraded fallback",
		"rule_id":"edge.agentd.degraded",
		"status":"blocked",
		"labels":{"source":"cordum-agentd"}
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("event write status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := sink.Len() - before; got != 2 {
		t.Fatalf("audit events emitted for agentd evidence = %d, want legacy edge + policy.decision.v2", got)
	}
	legacy := sink.Get(before)
	unified := sink.Get(before + 1)
	if legacy.EventType != audit.EventEdgeActionDenied {
		t.Fatalf("legacy event type = %q, want %q", legacy.EventType, audit.EventEdgeActionDenied)
	}
	if unified.EventType != audit.EventPolicyDecisionV2 ||
		unified.Extra["source"] != "edge" ||
		unified.Decision != "deny" ||
		unified.MatchedRule != "edge.agentd.degraded" {
		t.Fatalf("unified agentd event = type:%q source:%q decision:%q rule:%q extra:%#v",
			unified.EventType, unified.Extra["source"], unified.Decision, unified.MatchedRule, unified.Extra)
	}
}

func TestGatewayEdgeEventWriteDoesNotAuditNonDecisionEvents(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	sink := &testAuditSender{}
	s.auditExporter = sink
	session := createEdgeRouteSession(t, handler)
	before := sink.Len()

	rec := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-non-decision-no-audit",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-09T14:31:00Z",
		"layer":"hook",
		"kind":"hook.post_tool_use",
		"tool_name":"Bash",
		"input_redacted":{"summary":"done"},
		"decision":"`+string(edgecore.DecisionAllow)+`",
		"status":"ok"
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("event write status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := sink.Len() - before; got != 0 {
		t.Fatalf("non-decision audit events = %d, want 0", got)
	}
}
