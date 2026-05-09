package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	edgecore "github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestGatewayEdgeEvaluateConsumesBoundUnifiedEdgeRule(t *testing.T) {
	t.Setenv(audit.EnvUnifiedDecisionMode, string(audit.UnifiedDecisionModeDual))

	stub := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:         "legacy safety fallback",
		RuleId:         "legacy.allow",
		PolicySnapshot: "legacy-snapshot",
	}}
	s, handler := newEdgeEvaluateTestServer(t, stub)
	sink := &testAuditSender{}
	s.auditExporter = sink
	seedUnifiedEdgeBundle(t, s, "secops/edge-unified", map[string]any{
		"id":            "secops/edge-unified",
		"name":          "Unified edge bundle",
		"scope_binding": map[string]any{"kind": "global"},
		"metadata":      map[string]any{"edge_mode": "enforce"},
		"versions": []any{map[string]any{
			"version":     "edge-bundle-v9",
			"deployed_at": "2026-05-09T14:45:00Z",
			"rule_snapshot": []any{map[string]any{
				"id":      "unified.edge.deny.shell",
				"name":    "Deny shell from unified edge rule",
				"type":    "edge",
				"scope":   map[string]any{"kind": "global"},
				"status":  "published",
				"version": "v1",
				"audit":   map[string]any{"created_at": "2026-05-09T14:44:00Z", "created_by": "secops"},
				"match":   map[string]any{"capabilities": []any{"exec.shell"}},
				"decide":  map[string]any{"decision": "deny", "reason": "unified shell deny"},
			}},
		}},
	})
	session := createUnifiedEdgeBundleSession(t, handler, "secops/edge-unified")
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
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rec, &resp)
	if resp.Decision != string(edgecore.DecisionDeny) ||
		resp.RuleID != "unified.edge.deny.shell" ||
		resp.PolicySnapshot != "edge-bundle-v9" {
		t.Fatalf("response = decision:%q rule:%q snapshot:%q body=%s",
			resp.Decision, resp.RuleID, resp.PolicySnapshot, rec.Body.String())
	}
	if got := len(stub.capturedRequests()); got != 0 {
		t.Fatalf("legacy safety calls = %d, want 0 because bound unified edge rule matched", got)
	}
	if got := sink.Len() - before; got != 2 {
		t.Fatalf("audit events = %d, want legacy edge + policy.decision.v2", got)
	}
	unified := sink.Get(before + 1)
	if unified.EventType != audit.EventPolicyDecisionV2 ||
		unified.Extra["source"] != "edge" ||
		unified.Extra["bundle_id"] != "secops/edge-unified" ||
		unified.Decision != "deny" ||
		unified.MatchedRule != "unified.edge.deny.shell" {
		t.Fatalf("unified audit = type:%q source:%q bundle:%q decision:%q rule:%q extra:%#v",
			unified.EventType, unified.Extra["source"], unified.Extra["bundle_id"],
			unified.Decision, unified.MatchedRule, unified.Extra)
	}
}

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
	requireDeprecatedEndpointHeaders(t, rec, "/api/v1/policy/evaluate")
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

func TestGatewaySkipsAgentdFreshEvidenceWhenGatewayEvaluateAlreadyAudited(t *testing.T) {
	t.Setenv(audit.EnvUnifiedDecisionMode, string(audit.UnifiedDecisionModeDual))

	stub := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_DENY,
		Reason:         "fresh deny",
		RuleId:         "edge.deny.fresh",
		PolicySnapshot: "snap-fresh",
	}}
	s, handler := newEdgeEvaluateTestServer(t, stub)
	sink := &testAuditSender{}
	s.auditExporter = sink
	session := createEdgeRouteSession(t, handler)
	before := sink.Len()

	eval := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(
		session.SessionID,
		session.ExecutionID,
		edgeRouteTenant,
		"Bash",
		map[string]any{"command": "rm -rf /tmp/demo"},
	))
	if eval.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d body=%s", eval.Code, eval.Body.String())
	}
	if got := sink.Len() - before; got != 2 {
		t.Fatalf("audit events after evaluate = %d, want 2", got)
	}
	events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	if len(events) != 1 {
		t.Fatalf("persisted events after evaluate = %d, want 1: %#v", len(events), events)
	}
	gatewayEvent := events[0]

	rec := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"agentd-fresh-evidence",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-09T14:40:00Z",
		"layer":"hook",
		"kind":"hook.policy_decision",
		"agent_product":"cordum-agentd",
		"tool_name":"Bash",
		"action_name":"bash.exec",
		"capability":"exec.shell",
		"risk_tags":["exec"],
		"input_redacted":{"command":"rm -rf /tmp/demo"},
		"input_hash":"`+gatewayEvent.InputHash+`",
		"decision":"DENY",
		"decision_reason":"fresh deny",
		"rule_id":"edge.deny.fresh",
		"policy_snapshot":"snap-fresh",
		"status":"blocked",
		"labels":{
			"source":"cordum-agentd",
			"`+edgecore.LabelDecisionAuditEmittedBy+`":"`+edgecore.LabelDecisionAuditEmittedByGateway+`",
			"`+edgecore.LabelGatewayDecisionEventID+`":"`+gatewayEvent.EventID+`"
		}
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("event write status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := sink.Len() - before; got != 2 {
		t.Fatalf("audit events after agentd fresh evidence = %d, want still 2 (no duplicate)", got)
	}
	events = listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	if len(events) != 2 {
		t.Fatalf("persisted events after agentd evidence = %d, want gateway + agentd evidence", len(events))
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

func seedUnifiedEdgeBundle(t *testing.T, s *server, bundleID string, bundle map[string]any) {
	t.Helper()
	if bundle == nil {
		bundle = map[string]any{}
	}
	bundle["enabled"] = true
	err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.Scope(packs.PolicyConfigScope),
		ScopeID: packs.PolicyConfigID,
		Data: map[string]any{
			packs.PolicyConfigKey: map[string]any{
				bundleID: bundle,
			},
		},
	})
	if err != nil {
		t.Fatalf("seed unified edge bundle: %v", err)
	}
}

func createUnifiedEdgeBundleSession(t *testing.T, handler http.Handler, bundleID string) edgeSessionCreateResponseJSON {
	t.Helper()
	rec := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"mode":"local-dev",
		"policy_snapshot":"snap-edge-unified",
		"policy_mode":"observe",
		"labels":{"policy.bundle_id":"`+bundleID+`"}
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create unified bundle session status = %d body=%s", rec.Code, rec.Body.String())
	}
	var session edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, rec, &session)
	return session
}
