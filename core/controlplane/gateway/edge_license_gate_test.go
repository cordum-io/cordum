package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	edgeLicenseGateUpstreams    = "/api/v1/edge/mcp/upstreams"
	edgeLicenseGateGraceCode    = "license.grace_restricted"
	edgeLicenseGateDegradedCode = "license.degraded"
	edgeLicenseGateUpstreamBody = `{
		"name":"tenant-tools",
		"transport":"http",
		"endpoint":"https://mcp.example.com/tools",
		"auth_secret_ref":"secret://vault/mcp/tenant-tools",
		"labels":{"team":"platform"},
		"risk":"high",
		"enabled":true
	}`
)

// edgeLicenseGateState is a forced license state. writeRefusal and readRefusal hold the
// expected 503 code, or "" when the request answers.
type edgeLicenseGateState struct {
	name, status              string
	plan                      licensing.Plan
	rbac                      bool
	breakGlass                licensing.BreakGlassState
	writeRefusal, readRefusal string
	viewer, lifecycle         bool
}

var edgeLicenseGateStates = []edgeLicenseGateState{
	{"valid", "active", licensing.PlanEnterprise, true, licensing.BreakGlassStateValid, "", "", false, true},
	{"grace", "grace", licensing.PlanEnterprise, true, licensing.BreakGlassStateGrace, edgeLicenseGateGraceCode, "", false, true},
	{"team_valid", "active", licensing.PlanTeam, false, licensing.BreakGlassStateValid, "", "", false, false},
	{"team_grace", "grace", licensing.PlanTeam, false, licensing.BreakGlassStateGrace, edgeLicenseGateGraceCode, "", false, false},
	{"degraded", "fallback", licensing.PlanCommunity, false, licensing.BreakGlassStateInvalid, edgeLicenseGateDegradedCode, edgeLicenseGateDegradedCode, true, true},
	{"invalid", "fallback", licensing.PlanCommunity, false, licensing.BreakGlassStateInvalid, edgeLicenseGateDegradedCode, edgeLicenseGateDegradedCode, false, true},
	{"missing", "active", licensing.PlanCommunity, false, licensing.BreakGlassStateValid, "", "", false, true},
}

// TestEdgeRoutesLicenseGate pins the break-glass decision on an Edge write and an Edge read
// in every license state, and the agent decision path answering in every state.
func TestEdgeRoutesLicenseGate(t *testing.T) {
	for _, st := range edgeLicenseGateStates {
		t.Run(st.name, func(t *testing.T) {
			t.Run("write", func(t *testing.T) { edgeLicenseGateWrite(t, st) })
			t.Run("read", func(t *testing.T) { edgeLicenseGateRead(t, st) })
			if st.viewer {
				t.Run("viewer_write", func(t *testing.T) { edgeLicenseGateViewerWrite(t, st) })
			}
			if st.lifecycle {
				t.Run("lifecycle", func(t *testing.T) { edgeLicenseGateLifecycle(t, st) })
			}
		})
	}
}

// edgeLicenseGateServer forces st on a fresh Edge server whose policy answers DENY.
func edgeLicenseGateServer(t *testing.T, st edgeLicenseGateState) (*server, http.Handler, *fakeMCPUpstreamRegistry) {
	t.Helper()
	s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_DENY,
		Reason:         "blocked",
		PolicySnapshot: "snap-edge-license-gate",
		RuleId:         "edge-license-gate-deny",
	}})
	registry := &fakeMCPUpstreamRegistry{}
	s.mcpUpstreamRegistry = registry
	s.entitlements.ForceStateWithStatus(st.plan, licensing.DefaultEntitlements(st.plan), nil, st.status)
	if got := s.currentBreakGlassState(); got != st.breakGlass {
		t.Fatalf("break-glass state = %q, want %q", got, st.breakGlass)
	}
	if got := auth.RBACEntitled(s.currentEntitlements()); got != st.rbac {
		t.Fatalf("rbac entitled = %t, want %t", got, st.rbac)
	}
	return s, handler, registry
}

// edgeLicenseGateRefused checks that nothing reached the registry, then the break-glass 503 body.
func edgeLicenseGateRefused(t *testing.T, rr *httptest.ResponseRecorder, registry *fakeMCPUpstreamRegistry, st edgeLicenseGateState, code string) {
	t.Helper()
	if registry.createCalls != 0 || len(registry.entries) != 0 {
		t.Fatalf("refused request reached the registry: %d create calls, %d entries", registry.createCalls, len(registry.entries))
	}
	var body struct {
		Code      string `json:"code"`
		ErrorCode string `json:"error_code"`
		State     string `json:"state"`
	}
	if rr.Code != http.StatusServiceUnavailable || json.Unmarshal(rr.Body.Bytes(), &body) != nil ||
		body.Code != code || body.ErrorCode != code || body.State != string(st.breakGlass) {
		t.Fatalf("response = %d %s, want 503 with code and error_code %s and state %s", rr.Code, rr.Body.String(), code, st.breakGlass)
	}
}

func edgeLicenseGateWrite(t *testing.T, st edgeLicenseGateState) {
	_, handler, registry := edgeLicenseGateServer(t, st)
	rr := edgeRoutePOST(t, handler, edgeLicenseGateUpstreams, edgeLicenseGateUpstreamBody)
	if st.writeRefusal != "" {
		edgeLicenseGateRefused(t, rr, registry, st, st.writeRefusal)
		return
	}
	if registry.createCalls != 1 || len(registry.entries) != 1 ||
		registry.entries[0].Name != "tenant-tools" || registry.entries[0].TenantID != edgeRouteTenant {
		t.Fatalf("stored %d create calls %#v, want tenant-tools for %s", registry.createCalls, registry.entries, edgeRouteTenant)
	}
	if rr.Code != http.StatusCreated {
		t.Fatalf("write status = %d, want 201: %s", rr.Code, rr.Body.String())
	}
}

func edgeLicenseGateRead(t *testing.T, st edgeLicenseGateState) {
	_, handler, registry := edgeLicenseGateServer(t, st)
	rr := edgeRouteGET(t, handler, edgeLicenseGateUpstreams)
	if st.readRefusal != "" {
		edgeLicenseGateRefused(t, rr, registry, st, st.readRefusal)
		return
	}
	var page mcpUpstreamListResponse
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &page) != nil {
		t.Fatalf("read = %d %s, want 200 with the upstream list", rr.Code, rr.Body.String())
	}
}

// edgeLicenseGateViewerWrite checks that authorization answers before the license decision.
func edgeLicenseGateViewerWrite(t *testing.T, st edgeLicenseGateState) {
	_, handler, registry := edgeLicenseGateServer(t, st)
	rr := edgeRoutePOSTWithAPIKey(t, handler, edgeRouteViewerAPIKey, edgeLicenseGateUpstreams, edgeLicenseGateUpstreamBody)
	if registry.createCalls != 0 || len(registry.entries) != 0 {
		t.Fatalf("refused viewer write reached the registry: %d create calls, %d entries", registry.createCalls, len(registry.entries))
	}
	var body edgeErrorEnvelope
	if rr.Code != http.StatusForbidden || json.Unmarshal(rr.Body.Bytes(), &body) != nil || body.Code != edgeErrCodeAccessDenied {
		t.Fatalf("viewer write = %d %s, want 403 %s", rr.Code, rr.Body.String(), edgeErrCodeAccessDenied)
	}
}

// edgeLicenseGateLifecycle runs one agent lifecycle on the decision path. Every call answers
// 2xx, and the policy deny and the agent events are stored as decision evidence.
func edgeLicenseGateLifecycle(t *testing.T, st edgeLicenseGateState) {
	s, handler, _ := edgeLicenseGateServer(t, st)
	post := func(path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		rr := edgeRoutePOST(t, handler, path, body)
		if rr.Code != want {
			t.Fatalf("POST %s status = %d, want %d: %s", path, rr.Code, want, rr.Body.String())
		}
		return rr
	}
	session := createEdgeRouteSession(t, handler)
	post("/api/v1/edge/sessions/"+session.SessionID+"/heartbeat", `{}`, http.StatusOK)
	execution := createEdgeRouteExecution(t, handler, session.SessionID)

	var decision edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, post("/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, execution.ExecutionID, edgeRouteTenant,
		"Bash", map[string]any{"command": "rm -rf ./build"}), http.StatusOK), &decision)
	if decision.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("evaluate decision = %q, want %q", decision.Decision, edgecore.DecisionDeny)
	}
	events := listEdgeEvaluateEvents(t, s, session.SessionID, execution.ExecutionID)
	if len(events) != 1 || events[0].EventID != decision.EventID ||
		events[0].Kind != edgecore.EventKindHookPolicyDecision || events[0].Decision != edgecore.DecisionDeny {
		t.Fatalf("decision evidence = %#v, want the stored policy deny %q", events, decision.EventID)
	}

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	event := func(id string) string {
		return edgeEventJSON(id, session.SessionID, execution.ExecutionID, edgeRouteTenant, "", ts, "hook.post_tool_use", "ALLOW", "ok")
	}
	post("/api/v1/edge/events", event("evt-edge-license-gate-1"), http.StatusCreated)
	post("/api/v1/edge/events/batch", edgeEventBatchBody(event("evt-edge-license-gate-2"), event("evt-edge-license-gate-3")), http.StatusCreated)
	assertEdgeEventIDs(t, listEdgeEvaluateEvents(t, s, session.SessionID, execution.ExecutionID),
		[]string{decision.EventID, "evt-edge-license-gate-1", "evt-edge-license-gate-2", "evt-edge-license-gate-3"})
	post("/api/v1/edge/executions/"+execution.ExecutionID+"/end", `{"status":"succeeded"}`, http.StatusOK)
	post("/api/v1/edge/sessions/"+session.SessionID+"/end", `{"status":"ended"}`, http.StatusOK)
}
