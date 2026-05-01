package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
)

func TestGatewayEdgeEvaluateRouteRegisteredAndTenantScoped(t *testing.T) {
	s, _ := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	routes := make(map[string]routeInfo, len(s.Routes()))
	for _, route := range s.Routes() {
		routes[route.methodPathKey()] = route
	}

	got, ok := routes[http.MethodPost+" /api/v1/edge/evaluate"]
	if !ok {
		t.Fatal("missing Edge evaluate route registration for POST /api/v1/edge/evaluate")
	}
	if got.Auth == "public" {
		t.Fatal("Edge evaluate route was registered as public")
	}
	if got.Auth != "tenant" {
		t.Fatalf("Edge evaluate route auth = %q, want tenant", got.Auth)
	}
}

func TestGatewayEdgeEvaluateRequiresAuthTenantAndRejectsMalformedRequests(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})

	missingAuth := httptest.NewRequest(http.MethodPost, "/api/v1/edge/evaluate", strings.NewReader(`{}`))
	missingAuth.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, missingAuth)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d, want 401 body=%s", rr.Code, rr.Body.String())
	}

	missingTenant := httptest.NewRequest(http.MethodPost, "/api/v1/edge/evaluate", strings.NewReader(`{}`))
	addEdgeRouteAuth(missingTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, missingTenant)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing tenant status = %d, want 403 body=%s", rr.Code, rr.Body.String())
	}

	badJSON := httptest.NewRequest(http.MethodPost, "/api/v1/edge/evaluate", strings.NewReader(`{"session_id":`))
	addEdgeRouteAuth(badJSON)
	badJSON.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, badJSON)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}
	assertBodyOmits(t, rr.Body.String(), "enterprise_hook_token", "Bearer")

	mismatch := httptest.NewRequest(http.MethodPost, "/api/v1/edge/evaluate", strings.NewReader(`{"tenant_id":"`+edgeRouteOtherTenant+`"}`))
	addEdgeRouteAuth(mismatch)
	mismatch.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, mismatch)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("body tenant mismatch status = %d, want 403 body=%s", rr.Code, rr.Body.String())
	}
	assertBodyOmits(t, rr.Body.String(), edgeRouteOtherTenant)
}

func TestGatewayEdgeEvaluateAllowsTenantUserWithJobsWriteAndRejectsViewer(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{
		response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "user allowed"},
	})
	session := createEdgeEvaluateSessionWithAPIKey(t, handler, edgeRouteUserAPIKey)
	userBody := strings.Replace(
		edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}),
		`"principal_id":"principal-edge-a"`,
		`"principal_id":"principal-edge-user"`,
		1,
	)

	userEvaluate := edgeRoutePOSTWithAPIKey(t, handler, edgeRouteUserAPIKey, "/api/v1/edge/evaluate", userBody)
	if userEvaluate.Code != http.StatusOK {
		t.Fatalf("user evaluate status = %d, want 200 body=%s", userEvaluate.Code, userEvaluate.Body.String())
	}
	var userResp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, userEvaluate, &userResp)
	if userResp.PermissionDecision != "allow" {
		t.Fatalf("user evaluate permission_decision = %q, want allow", userResp.PermissionDecision)
	}

	viewerEvaluate := edgeRoutePOSTWithAPIKey(t, handler, edgeRouteViewerAPIKey, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}))
	if viewerEvaluate.Code != http.StatusForbidden {
		t.Fatalf("viewer evaluate status = %d, want 403 body=%s", viewerEvaluate.Code, viewerEvaluate.Body.String())
	}
}

func TestGatewayEdgeEvaluateRejectsMissingCrossTenantAndTerminalParents(t *testing.T) {
	stub := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "ok"}}
	s, handler := newEdgeEvaluateTestServer(t, stub)
	session := createEdgeRouteSession(t, handler)

	missing := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody("missing-session", "missing-execution", edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing parents status = %d, want 404 body=%s", missing.Code, missing.Body.String())
	}
	assertBodyOmits(t, missing.Body.String(), "missing-session", "missing-execution", "npm test")

	crossTenant := httptest.NewRequest(http.MethodPost, "/api/v1/edge/evaluate", strings.NewReader(edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteOtherTenant, "Bash", map[string]any{"command": "echo Bearer cross-tenant-secret"})))
	addEdgeRouteAuthFor(crossTenant, edgeRouteOtherAPIKey)
	crossTenant.Header.Set("X-Tenant-ID", edgeRouteOtherTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, crossTenant)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status = %d, want 404 body=%s", rr.Code, rr.Body.String())
	}
	assertBodyOmits(t, rr.Body.String(), session.SessionID, session.ExecutionID, "cross-tenant-secret", edgeRouteTenant)

	otherSession := createEdgeRouteSession(t, handler)
	mismatchedExecution := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, otherSession.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}))
	if mismatchedExecution.Code != http.StatusBadRequest {
		t.Fatalf("mismatched execution status = %d, want 400 body=%s", mismatchedExecution.Code, mismatchedExecution.Body.String())
	}
	assertBodyOmits(t, mismatchedExecution.Body.String(), otherSession.ExecutionID)

	endedAt := session.Session.StartedAt.Add(1)
	if _, err := s.edgeStore.EndSession(context.Background(), edgeRouteTenant, session.SessionID, endedAt, edgecore.SessionStatusEnded); err != nil {
		t.Fatalf("end session fixture: %v", err)
	}
	ended := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "echo Bearer ended-session-secret"}))
	if ended.Code != http.StatusConflict {
		t.Fatalf("ended session status = %d, want 409 body=%s", ended.Code, ended.Body.String())
	}
	assertBodyOmits(t, ended.Body.String(), "ended-session-secret")

	terminalSession := createEdgeRouteSession(t, handler)
	if _, err := s.edgeStore.EndExecution(context.Background(), edgeRouteTenant, terminalSession.ExecutionID, terminalSession.Execution.StartedAt.Add(1), edgecore.ExecutionStatusFailed); err != nil {
		t.Fatalf("end execution fixture: %v", err)
	}
	terminal := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(terminalSession.SessionID, terminalSession.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}))
	if terminal.Code != http.StatusConflict {
		t.Fatalf("terminal execution status = %d, want 409 body=%s", terminal.Code, terminal.Body.String())
	}
}

func TestGatewayEdgeEvaluateMapsSafetyDecisionsToHookResponse(t *testing.T) {
	for _, tc := range []struct {
		name               string
		safety             *pb.PolicyCheckResponse
		wantDecision       string
		wantPermission     string
		wantExitCode       int
		wantApprovalRef    string
		wantWaitStrategy   string
		wantConstraints    bool
		wantTerminalSubstr string
	}{
		{
			name:           "allow",
			safety:         &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "safe", PolicySnapshot: "snap-allow", RuleId: "allow-rule"},
			wantDecision:   "ALLOW",
			wantPermission: "allow",
			wantExitCode:   0,
		},
		{
			name:               "deny",
			safety:             &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "blocked", PolicySnapshot: "snap-deny", RuleId: "deny-rule"},
			wantDecision:       "DENY",
			wantPermission:     "deny",
			wantExitCode:       2,
			wantTerminalSubstr: "blocked",
		},
		{
			name:               "require approval",
			safety:             &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, Reason: "needs approval", PolicySnapshot: "snap-approval", RuleId: "approval-rule", ApprovalRequired: true, ApprovalRef: "approval-edge-1"},
			wantDecision:       "REQUIRE_APPROVAL",
			wantPermission:     "deny",
			wantExitCode:       2,
			wantApprovalRef:    edgecore.ApprovalRefPrefix,
			wantWaitStrategy:   "manual_approval",
			wantTerminalSubstr: "approval",
		},
		{
			name:               "throttle",
			safety:             &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_THROTTLE, Reason: "slow down", PolicySnapshot: "snap-throttle", RuleId: "throttle-rule"},
			wantDecision:       "THROTTLE",
			wantPermission:     "deny",
			wantExitCode:       2,
			wantWaitStrategy:   "backoff",
			wantTerminalSubstr: "slow down",
		},
		{
			name: "constrain",
			safety: &pb.PolicyCheckResponse{
				Decision:       pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS,
				Reason:         "allowed with constraints",
				PolicySnapshot: "snap-constrain",
				RuleId:         "constraint-rule",
				Constraints: &pb.PolicyConstraints{
					Toolchain: &pb.ToolchainConstraints{AllowedCommands: []string{"npm test"}},
				},
			},
			wantDecision:    "CONSTRAIN",
			wantPermission:  "allow",
			wantExitCode:    0,
			wantConstraints: true,
		},
		{
			name:               "unspecified fail closed",
			safety:             &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_UNSPECIFIED, Reason: "unknown"},
			wantDecision:       "DENY",
			wantPermission:     "deny",
			wantExitCode:       2,
			wantTerminalSubstr: "unknown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{response: tc.safety})
			session := createEdgeRouteSession(t, handler)
			if tc.safety.GetApprovalRequired() || tc.safety.GetDecision() == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
				tc.safety.PolicySnapshot = session.PolicySnapshot
			}

			rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}))
			if rr.Code != http.StatusOK {
				t.Fatalf("evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
			}
			var resp edgeEvaluateResponseJSON
			decodeEdgeRouteJSON(t, rr, &resp)
			if resp.Decision != tc.wantDecision {
				t.Fatalf("decision = %q, want %q body=%s", resp.Decision, tc.wantDecision, rr.Body.String())
			}
			if resp.PermissionDecision != tc.wantPermission {
				t.Fatalf("permission_decision = %q, want %q body=%s", resp.PermissionDecision, tc.wantPermission, rr.Body.String())
			}
			if resp.ExitCode != tc.wantExitCode {
				t.Fatalf("exit_code = %d, want %d body=%s", resp.ExitCode, tc.wantExitCode, rr.Body.String())
			}
			if tc.wantApprovalRef == edgecore.ApprovalRefPrefix {
				if !strings.HasPrefix(resp.ApprovalRef, edgecore.ApprovalRefPrefix) {
					t.Fatalf("approval_ref = %q, want generated %q prefix body=%s", resp.ApprovalRef, edgecore.ApprovalRefPrefix, rr.Body.String())
				}
			} else if resp.ApprovalRef != tc.wantApprovalRef {
				t.Fatalf("approval_ref = %q, want %q body=%s", resp.ApprovalRef, tc.wantApprovalRef, rr.Body.String())
			}
			if resp.WaitStrategy != tc.wantWaitStrategy {
				t.Fatalf("wait_strategy = %q, want %q body=%s", resp.WaitStrategy, tc.wantWaitStrategy, rr.Body.String())
			}
			if tc.wantConstraints && len(resp.Constraints) == 0 {
				t.Fatalf("constraints empty, want safety constraints body=%s", rr.Body.String())
			}
			if tc.wantTerminalSubstr != "" && !strings.Contains(strings.ToLower(resp.TerminalMessage), tc.wantTerminalSubstr) {
				t.Fatalf("terminal_message = %q, want substring %q", resp.TerminalMessage, tc.wantTerminalSubstr)
			}
		})
	}
}

func TestGatewayEdgeEvaluateRequireApprovalResponseIncludesRetryMetadata(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "production edit requires approval",
		RuleId:           "claude-code.prod-edit-approval",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{
		"command": "echo Bearer edge-approval-secret && npm test",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if resp.Decision != string(edgecore.DecisionRequireApproval) {
		t.Fatalf("decision = %q, want REQUIRE_APPROVAL body=%s", resp.Decision, rr.Body.String())
	}
	if resp.Reason != "production edit requires approval" || resp.RuleID != "claude-code.prod-edit-approval" || resp.PolicySnapshot != session.PolicySnapshot {
		t.Fatalf("policy fields = reason:%q rule:%q snapshot:%q body=%s", resp.Reason, resp.RuleID, resp.PolicySnapshot, rr.Body.String())
	}
	if !strings.HasPrefix(resp.ApprovalRef, edgecore.ApprovalRefPrefix) {
		t.Fatalf("approval_ref = %q, want generated %q prefix body=%s", resp.ApprovalRef, edgecore.ApprovalRefPrefix, rr.Body.String())
	}
	if resp.ApprovalURL != "/edge/approvals/"+resp.ApprovalRef {
		t.Fatalf("approval_url = %q, want dashboard path for approval_ref %q", resp.ApprovalURL, resp.ApprovalRef)
	}
	if resp.ActionHash == "" || !strings.HasPrefix(resp.ActionHash, "sha256:") {
		t.Fatalf("action_hash = %q, want server-generated sha256 binding", resp.ActionHash)
	}
	if resp.InputHash == "" || !strings.HasPrefix(resp.InputHash, "sha256:") {
		t.Fatalf("input_hash = %q, want server-computed sha256 binding", resp.InputHash)
	}
	if resp.WaitStrategy != "manual_approval" || resp.WaitAfter != "approve_then_retry" {
		t.Fatalf("wait guidance = strategy:%q wait_after:%q, want manual_approval/approve_then_retry body=%s", resp.WaitStrategy, resp.WaitAfter, rr.Body.String())
	}
	if resp.PermissionDecision != "deny" || resp.ExitCode != 2 {
		t.Fatalf("hook permission/exit = %q/%d, want deny/2 body=%s", resp.PermissionDecision, resp.ExitCode, rr.Body.String())
	}
	for _, want := range []string{"not run", resp.ApprovalRef, "approve", "retry"} {
		if !strings.Contains(strings.ToLower(resp.TerminalMessage), strings.ToLower(want)) {
			t.Fatalf("terminal_message = %q, want substring %q", resp.TerminalMessage, want)
		}
		if !strings.Contains(strings.ToLower(resp.PermissionDecisionReason), strings.ToLower(want)) {
			t.Fatalf("permission_decision_reason = %q, want substring %q", resp.PermissionDecisionReason, want)
		}
	}
	assertBodyOmits(t, rr.Body.String(), "edge-approval-secret")

	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, resp.ApprovalRef)
	if err != nil || !ok {
		t.Fatalf("GetApproval(%q) = (%#v,%v,%v), want stored pending approval", resp.ApprovalRef, stored, ok, err)
	}
	if stored.Status != edgecore.ApprovalStatusPending ||
		stored.EventID != resp.EventID ||
		stored.ActionHash != resp.ActionHash ||
		stored.InputHash != resp.InputHash ||
		stored.PolicySnapshot != resp.PolicySnapshot {
		t.Fatalf("stored approval binding = status:%q event:%q action:%q input:%q snapshot:%q, want response binding %#v",
			stored.Status, stored.EventID, stored.ActionHash, stored.InputHash, stored.PolicySnapshot, resp)
	}
}

func TestGatewayEdgeEvaluateSafetyUnavailableByPolicyMode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		policyMode     edgecore.PolicyMode
		command        string
		wantDecision   string
		wantPermission string
		wantDegraded   bool
	}{
		{
			name:           "observe degrades open with evidence warning",
			policyMode:     edgecore.PolicyModeObserve,
			command:        "rm -rf ./tmp/edge-observe",
			wantDecision:   "ALLOW",
			wantPermission: "allow",
			wantDegraded:   true,
		},
		{
			name:           "enforce high risk fails closed",
			policyMode:     edgecore.PolicyModeEnforce,
			command:        "rm -rf ./tmp/edge-enforce",
			wantDecision:   "DENY",
			wantPermission: "deny",
			wantDegraded:   true,
		},
		{
			name:           "enterprise strict fails closed even for low risk",
			policyMode:     edgecore.PolicyModeEnterpriseStrict,
			command:        "npm test",
			wantDecision:   "DENY",
			wantPermission: "deny",
			wantDegraded:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{err: errors.New("safety unavailable: Bearer safety-secret")})
			session := createEdgeEvaluateSessionWithPolicyMode(t, handler, tc.policyMode)

			rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": tc.command}))
			if rr.Code != http.StatusOK {
				t.Fatalf("evaluate status = %d, want hook-friendly 200 body=%s", rr.Code, rr.Body.String())
			}
			var resp edgeEvaluateResponseJSON
			decodeEdgeRouteJSON(t, rr, &resp)
			if resp.Decision != tc.wantDecision {
				t.Fatalf("decision = %q, want %q body=%s", resp.Decision, tc.wantDecision, rr.Body.String())
			}
			if resp.PermissionDecision != tc.wantPermission {
				t.Fatalf("permission_decision = %q, want %q body=%s", resp.PermissionDecision, tc.wantPermission, rr.Body.String())
			}
			if resp.Degraded != tc.wantDegraded {
				t.Fatalf("degraded = %v, want %v body=%s", resp.Degraded, tc.wantDegraded, rr.Body.String())
			}
			if resp.ErrorCode != "safety_unavailable" {
				t.Fatalf("error_code = %q, want safety_unavailable body=%s", resp.ErrorCode, rr.Body.String())
			}
			assertBodyOmits(t, rr.Body.String(), "safety-secret")
		})
	}
}

func TestGatewayEdgeEvaluatePersistsDecisionEventWithRedactedInput(t *testing.T) {
	s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_DENY,
		Reason:         "secret access blocked",
		PolicySnapshot: "snap-decision",
		RuleId:         "deny-secret-command",
		ApprovalRef:    "approval-readonly-reference",
	}})
	session := createEdgeRouteSession(t, handler)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{
		"command": "echo Authorization: Bearer edge-persist-secret",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if strings.TrimSpace(resp.EventID) == "" {
		t.Fatalf("event_id empty in response body=%s", rr.Body.String())
	}

	events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	if len(events) != 1 {
		t.Fatalf("persisted events = %d, want exactly 1: %#v", len(events), events)
	}
	event := events[0]
	if event.EventID != resp.EventID {
		t.Fatalf("persisted event_id = %q, response event_id = %q", event.EventID, resp.EventID)
	}
	if event.Kind != edgecore.EventKindHookPolicyDecision {
		t.Fatalf("event kind = %q, want %q", event.Kind, edgecore.EventKindHookPolicyDecision)
	}
	if event.Decision != edgecore.DecisionDeny || event.Status != edgecore.ActionStatusBlocked {
		t.Fatalf("event decision/status = %q/%q, want DENY/blocked", event.Decision, event.Status)
	}
	if got := event.InputRedacted["command"]; got != "<redacted>" {
		t.Fatalf("event input_redacted command = %#v, want <redacted>", got)
	}
	if event.InputHash == "" || !strings.HasPrefix(event.InputHash, "sha256:") {
		t.Fatalf("event input_hash = %q, want sha256 hash", event.InputHash)
	}
	if event.DurationMS <= 0 {
		t.Fatalf("event duration_ms = %d, want > 0", event.DurationMS)
	}
	if event.RuleID != "deny-secret-command" || event.PolicySnapshot != "snap-decision" || event.ApprovalRef != "approval-readonly-reference" {
		t.Fatalf("policy fields = rule:%q snapshot:%q approval:%q", event.RuleID, event.PolicySnapshot, event.ApprovalRef)
	}
	if event.ActionName != "bash.exec" || event.Capability != "exec.shell" {
		t.Fatalf("classification fields = action:%q capability:%q", event.ActionName, event.Capability)
	}
	assertBodyOmits(t, rr.Body.String(), "edge-persist-secret")
}

func TestGatewayEdgeEvaluateStreamsOnlyPersistedDecisionEvents(t *testing.T) {
	s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_DENY,
		Reason:         "stream blocked",
		PolicySnapshot: "snap-stream",
		RuleId:         "deny-stream",
	}})
	session := createEdgeRouteSession(t, handler)
	drainGatewayEdgeStreamQueue(s.eventsCh)
	streamQueue := &wsClient{ch: s.eventsCh}

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{
		"command": "echo Bearer edge-stream-secret",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)

	streamed := readGatewayEdgeStreamEvent(t, streamQueue, "evaluate policy decision edge.event")
	if streamed.tenant != edgeRouteTenant {
		t.Fatalf("stream tenant = %q, want %q", streamed.tenant, edgeRouteTenant)
	}
	var envelope struct {
		Type  string                    `json:"type"`
		Event edgecore.AgentActionEvent `json:"event"`
	}
	if err := json.Unmarshal(streamed.data, &envelope); err != nil {
		t.Fatalf("decode streamed evaluate edge.event: %v body=%s", err, string(streamed.data))
	}
	if envelope.Type != "edge.event" || envelope.Event.EventID != resp.EventID || envelope.Event.Kind != edgecore.EventKindHookPolicyDecision {
		t.Fatalf("stream envelope = type %q event %q kind %q, want edge.event/%q/%q",
			envelope.Type, envelope.Event.EventID, envelope.Event.Kind, resp.EventID, edgecore.EventKindHookPolicyDecision)
	}
	assertBodyOmits(t, string(streamed.data), "edge-stream-secret")
	assertNoGatewayEdgeStreamEvent(t, streamQueue, "evaluate should stream exactly the persisted decision event")
}

func TestGatewayEdgeEvaluateDoesNotStreamWhenPersistenceFails(t *testing.T) {
	s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:   "allow before append failure",
	}})
	session := createEdgeRouteSession(t, handler)
	drainGatewayEdgeStreamQueue(s.eventsCh)
	streamQueue := &wsClient{ch: s.eventsCh}
	s.edgeStore = edgeEvaluateFailingAppendStore{Store: s.edgeStore}

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{
		"command": "echo Bearer edge-append-failure-secret",
	}))
	if rr.Code != http.StatusInternalServerError && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("append failure status = %d, want sanitized 5xx body=%s", rr.Code, rr.Body.String())
	}
	assertBodyOmits(t, rr.Body.String(), "edge-append-failure-secret", "append-failure-secret")
	assertNoGatewayEdgeStreamEvent(t, streamQueue, "failed evaluate persistence must not stream phantom edge.event")
}

func TestGatewayEdgeEvaluatePersistsDegradedEventForSafetyUnavailable(t *testing.T) {
	s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{err: errors.New("safety down: Bearer edge-degraded-secret")})
	session := createEdgeEvaluateSessionWithPolicyMode(t, handler, edgecore.PolicyModeObserve)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if !resp.Degraded || resp.ErrorCode != "safety_unavailable" {
		t.Fatalf("response degraded/error = %v/%q, want true/safety_unavailable body=%s", resp.Degraded, resp.ErrorCode, rr.Body.String())
	}

	events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	if len(events) != 1 {
		t.Fatalf("persisted events = %d, want exactly 1: %#v", len(events), events)
	}
	event := events[0]
	if event.Kind != edgecore.EventKindPolicyDegraded {
		t.Fatalf("event kind = %q, want %q", event.Kind, edgecore.EventKindPolicyDegraded)
	}
	if event.Status != edgecore.ActionStatusDegraded {
		t.Fatalf("event status = %q, want degraded", event.Status)
	}
	if event.Decision == edgecore.DecisionAllow {
		t.Fatal("degraded event recorded false ALLOW decision")
	}
	if event.ErrorCode != "safety_unavailable" || strings.Contains(event.ErrorMessage, "edge-degraded-secret") {
		t.Fatalf("event error fields = %q/%q, want sanitized safety_unavailable", event.ErrorCode, event.ErrorMessage)
	}
	if event.DurationMS <= 0 {
		t.Fatalf("event duration_ms = %d, want > 0", event.DurationMS)
	}
}

func TestGatewayEdgeEvaluateRejectsRawAndOversizeInputWithoutPersistence(t *testing.T) {
	s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}})
	session := createEdgeRouteSession(t, handler)

	raw := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", `{
		"tenant_id":"`+edgeRouteTenant+`",
		"principal_id":"principal-edge-a",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"agent_product":"claude-code",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"tool_name":"Bash",
		"tool_input":{"command":"echo Bearer edge-raw-secret"}
	}`)
	if raw.Code != http.StatusBadRequest {
		t.Fatalf("raw payload status = %d, want 400 body=%s", raw.Code, raw.Body.String())
	}
	assertBodyOmits(t, raw.Body.String(), "edge-raw-secret")
	if events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID); len(events) != 0 {
		t.Fatalf("raw payload persisted events = %#v, want none", events)
	}

	oversizeValue := strings.Repeat("x", edgecore.MaxInputRedactedBytes+1024)
	oversize := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": oversizeValue}))
	if oversize.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize input status = %d, want 413 body=%s", oversize.Code, oversize.Body.String())
	}
	if events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID); len(events) != 0 {
		t.Fatalf("oversize payload persisted events = %#v, want none", events)
	}
}

func TestBuildEdgeEvaluatePolicyInputUsesClassifierAndMapper(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}})
	session := createEdgeRouteSession(t, handler)

	input, err := buildEdgeEvaluatePolicyInput(edgeEvaluateContext{
		req: edgeEvaluateRequest{
			TenantID:          edgeRouteTenant,
			PrincipalID:       "principal-edge-a",
			SessionID:         session.SessionID,
			ExecutionID:       session.ExecutionID,
			AgentProduct:      "claude-code",
			Layer:             edgecore.LayerHook,
			Kind:              edgecore.EventKindHookPreToolUse,
			ToolName:          "Bash",
			InputRedacted:     map[string]any{"command": "rm -rf ./tmp/edge-evaluate"},
			ActionName:        "client.spoofed",
			Capability:        "client.spoofed",
			RiskTags:          []string{"safe"},
			Labels:            edgecore.Labels{"edge.action_name": "client-spoofed", "custom.team": "platform"},
			ArtifactPointers:  nil,
			ToolInputRedacted: nil,
			ToolInputHash:     "client-hash-should-be-overwritten",
			InputHash:         "client-hash-should-be-overwritten",
		},
		tenantID:    edgeRouteTenant,
		principalID: "principal-edge-a",
		session:     &session.Session,
		execution:   &session.Execution,
	})
	if err != nil {
		t.Fatalf("buildEdgeEvaluatePolicyInput returned error: %v", err)
	}
	if input.event.ActionName != "bash.exec" || input.event.Capability != "exec.shell" {
		t.Fatalf("event classification fields = %q/%q, want bash.exec/exec.shell", input.event.ActionName, input.event.Capability)
	}
	if input.event.InputHash == "" || !strings.HasPrefix(input.event.InputHash, "sha256:") || strings.Contains(input.event.InputHash, "client-hash") {
		t.Fatalf("event input_hash = %q, want server-computed sha256", input.event.InputHash)
	}
	if got := input.policyRequest.GetTopic(); got != edgecore.EdgePolicyTopic {
		t.Fatalf("policy topic = %q, want %q", got, edgecore.EdgePolicyTopic)
	}
	if got := input.policyRequest.GetMeta().GetCapability(); got != "exec.shell" {
		t.Fatalf("policy capability = %q, want classifier capability", got)
	}
	if !edgeEvaluateStringSliceContains(input.policyRequest.GetMeta().GetRiskTags(), "destructive") ||
		!edgeEvaluateStringSliceContains(input.policyRequest.GetMeta().GetRiskTags(), "filesystem") ||
		edgeEvaluateStringSliceContains(input.policyRequest.GetMeta().GetRiskTags(), "safe") {
		t.Fatalf("policy risk tags = %#v, want classifier destructive/filesystem and no client safe tag", input.policyRequest.GetMeta().GetRiskTags())
	}
	if got := input.policyRequest.GetLabels()["edge.action_name"]; got != "bash.exec" {
		t.Fatalf("policy edge.action_name label = %q, want bash.exec in %#v", got, input.policyRequest.GetLabels())
	}
	if got := input.policyRequest.GetLabels()["custom.team"]; got != "platform" {
		t.Fatalf("custom label not preserved: %#v", input.policyRequest.GetLabels())
	}
	if strings.Contains(string(input.policyRequest.GetInputContent()), "client.spoofed") {
		t.Fatalf("policy input content leaked client spoofed classification: %s", string(input.policyRequest.GetInputContent()))
	}
}

func TestEdgeEvaluateMergeLabelsRejectsOversizeBeforeAllocation(t *testing.T) {
	base := make(edgecore.Labels, edgecore.MaxLabelEntries)
	for i := 0; i < edgecore.MaxLabelEntries; i++ {
		base["base.label."+strconv.Itoa(i)] = "ok"
	}
	_, err := edgeEvaluateMergeLabels(base, edgecore.Labels{"overflow": "true"})
	if err == nil {
		t.Fatal("edgeEvaluateMergeLabels oversize error = nil, want request rejection before allocation")
	}
	var requestErr edgeEventRequestError
	if !errors.As(err, &requestErr) || requestErr.status != http.StatusBadRequest {
		t.Fatalf("edgeEvaluateMergeLabels oversize error = %T %v, want bad request edgeEventRequestError", err, err)
	}
}

func TestGatewayEdgeEvaluateAppliesDemoPolicySimulationFixtures(t *testing.T) {
	safety := &edgeEvaluatePolicySafetyClient{
		policy:   loadEdgeEvaluateDemoPolicy(t),
		snapshot: "edge-demo-policy-gateway-test",
	}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	fixtures := loadEdgeEvaluatePolicySimulationFixtures(t)

	for _, name := range []string{"bash_rm_rf", "read_dotenv", "bash_npm_test", "edit_source"} {
		tc, ok := fixtures[name]
		if !ok {
			t.Fatalf("missing fixture case %q", name)
		}
		t.Run(name, func(t *testing.T) {
			session := createEdgeEvaluateSessionWithPolicySnapshot(t, handler, safety.snapshot, edgecore.PolicyModeObserve)
			rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBodyFromFixture(session.SessionID, session.ExecutionID, tc))
			if rr.Code != http.StatusOK {
				t.Fatalf("evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
			}

			var resp edgeEvaluateResponseJSON
			decodeEdgeRouteJSON(t, rr, &resp)
			wantDecision := edgeEvaluateResponseDecisionForPolicyDecision(tc.ExpectedDecision)
			if resp.Decision != wantDecision {
				t.Fatalf("decision = %q, want %q body=%s", resp.Decision, wantDecision, rr.Body.String())
			}
			if resp.RuleID != tc.ExpectedRuleID {
				t.Fatalf("rule_id = %q, want %q body=%s", resp.RuleID, tc.ExpectedRuleID, rr.Body.String())
			}
			if resp.PolicySnapshot != "edge-demo-policy-gateway-test" {
				t.Fatalf("policy_snapshot = %q, want edge-demo-policy-gateway-test", resp.PolicySnapshot)
			}
			wantPermission := "deny"
			wantExitCode := 2
			if tc.ExpectedDecision == "ALLOW" {
				wantPermission = "allow"
				wantExitCode = 0
			}
			if resp.PermissionDecision != wantPermission || resp.ExitCode != wantExitCode {
				t.Fatalf("hook permission/exit = %q/%d, want %q/%d body=%s", resp.PermissionDecision, resp.ExitCode, wantPermission, wantExitCode, rr.Body.String())
			}
			if tc.ExpectedApprovalRequired && resp.WaitStrategy != "manual_approval" {
				t.Fatalf("wait_strategy = %q, want manual_approval body=%s", resp.WaitStrategy, rr.Body.String())
			}

			events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
			if len(events) != 1 {
				t.Fatalf("persisted events = %d, want exactly one Edge decision event: %#v", len(events), events)
			}
			event := events[0]
			if event.Kind != edgecore.EventKindHookPolicyDecision {
				t.Fatalf("event kind = %q, want %q", event.Kind, edgecore.EventKindHookPolicyDecision)
			}
			if event.RuleID != tc.ExpectedRuleID || event.PolicySnapshot != "edge-demo-policy-gateway-test" {
				t.Fatalf("event policy fields = rule:%q snapshot:%q, want %q/edge-demo-policy-gateway-test", event.RuleID, event.PolicySnapshot, tc.ExpectedRuleID)
			}
			if event.Decision != edgeEvaluateEventDecisionForPolicyDecision(tc.ExpectedDecision) {
				t.Fatalf("event decision = %q, want policy decision %q", event.Decision, tc.ExpectedDecision)
			}
		})
	}

	for _, req := range safety.capturedRequests() {
		if req.GetJobId() != "" {
			t.Fatalf("Edge evaluate policy request unexpectedly set job_id %q; Edge actions must not become Cordum Jobs", req.GetJobId())
		}
		if req.GetTopic() != edgecore.EdgePolicyTopic {
			t.Fatalf("policy request topic = %q, want %q", req.GetTopic(), edgecore.EdgePolicyTopic)
		}
	}
}

func newEdgeEvaluateTestServer(t *testing.T, safety pb.SafetyKernelClient) (*server, http.Handler) {
	t.Helper()
	s, handler := newEdgeRouteTestServer(t)
	s.safetyClient = safety
	return s, handler
}

func listEdgeEvaluateEvents(t *testing.T, s *server, sessionID, executionID string) []edgecore.AgentActionEvent {
	t.Helper()
	page, err := s.edgeStore.ListEvents(context.Background(), edgecore.ListEventsQuery{
		TenantID:    edgeRouteTenant,
		SessionID:   sessionID,
		ExecutionID: executionID,
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return page.Items
}

func createEdgeEvaluateSessionWithPolicyMode(t *testing.T, handler http.Handler, mode edgecore.PolicyMode) edgeSessionCreateResponseJSON {
	t.Helper()
	return createEdgeEvaluateSessionWithPolicySnapshot(t, handler, "snap-edge-evaluate", mode)
}

func createEdgeEvaluateSessionWithPolicySnapshot(t *testing.T, handler http.Handler, snapshot string, mode edgecore.PolicyMode) edgeSessionCreateResponseJSON {
	t.Helper()
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"policy_snapshot":"`+snapshot+`",
		"policy_mode":"`+string(mode)+`"
	}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create evaluate session status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var session edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, rr, &session)
	return session
}

func createEdgeEvaluateSessionWithAPIKey(t *testing.T, handler http.Handler, apiKey string) edgeSessionCreateResponseJSON {
	t.Helper()
	rr := edgeRoutePOSTWithAPIKey(t, handler, apiKey, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"policy_snapshot":"snap-edge-evaluate-user",
		"policy_mode":"observe"
	}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create evaluate user session status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var session edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, rr, &session)
	return session
}

func edgeRoutePOSTWithAPIKey(t *testing.T, handler http.Handler, apiKey, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	addEdgeRouteAuthFor(req, apiKey)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

type edgeEvaluateResponseJSON struct {
	Decision                 string         `json:"decision"`
	Reason                   string         `json:"reason"`
	RuleID                   string         `json:"rule_id"`
	PolicySnapshot           string         `json:"policy_snapshot"`
	ApprovalRef              string         `json:"approval_ref"`
	ApprovalURL              string         `json:"approval_url"`
	ActionHash               string         `json:"action_hash"`
	InputHash                string         `json:"input_hash"`
	Constraints              map[string]any `json:"constraints"`
	UpdatedInput             map[string]any `json:"updated_input"`
	EventID                  string         `json:"event_id"`
	Degraded                 bool           `json:"degraded"`
	ErrorCode                string         `json:"error_code"`
	ErrorMessage             string         `json:"error_message"`
	PermissionDecision       string         `json:"permission_decision"`
	PermissionDecisionReason string         `json:"permission_decision_reason"`
	ExitCode                 int            `json:"exit_code"`
	TerminalTitle            string         `json:"terminal_title"`
	TerminalMessage          string         `json:"terminal_message"`
	WaitStrategy             string         `json:"wait_strategy"`
	WaitAfter                string         `json:"wait_after"`
	TimeoutMS                int            `json:"timeout_ms"`
}

func edgeEvaluateBody(sessionID, executionID, tenantID, toolName string, input map[string]any) string {
	command := ""
	if value, ok := input["command"].(string); ok {
		encoded, _ := json.Marshal(value)
		command = string(encoded)
	} else {
		command = `""`
	}
	return `{
		"tenant_id":"` + tenantID + `",
		"principal_id":"principal-edge-a",
		"session_id":"` + sessionID + `",
		"execution_id":"` + executionID + `",
		"agent_product":"claude-code",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"tool_name":"` + toolName + `",
		"input_redacted":{"command":` + command + `}
	}`
}

func edgeEvaluateBodyFromFixture(sessionID, executionID string, tc edgeEvaluatePolicySimulationCase) string {
	body := map[string]any{
		"tenant_id":      edgeRouteTenant,
		"principal_id":   "principal-edge-a",
		"session_id":     sessionID,
		"execution_id":   executionID,
		"agent_product":  tc.Event.AgentProduct,
		"layer":          tc.Event.Layer,
		"kind":           tc.Event.Kind,
		"tool_name":      tc.Event.ToolName,
		"input_redacted": tc.Event.InputRedacted,
	}
	data, _ := json.Marshal(body)
	return string(data)
}

func edgeEvaluateStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type edgeEvaluateStubSafetyClient struct {
	mu       sync.Mutex
	requests []*pb.PolicyCheckRequest
	response *pb.PolicyCheckResponse
	err      error
}

type edgeEvaluatePolicySimulationFixture struct {
	Cases []edgeEvaluatePolicySimulationCase `json:"cases"`
}

type edgeEvaluatePolicySimulationCase struct {
	Name                     string                    `json:"name"`
	Event                    edgecore.AgentActionEvent `json:"event"`
	ExpectedDecision         string                    `json:"expected_decision"`
	ExpectedRuleID           string                    `json:"expected_rule_id"`
	ExpectedApprovalRequired bool                      `json:"expected_approval_required"`
}

type edgeEvaluatePolicySafetyClient struct {
	mu       sync.Mutex
	policy   *config.SafetyPolicy
	snapshot string
	requests []*pb.PolicyCheckRequest
}

type edgeEvaluateFailingAppendStore struct {
	edgecore.Store
}

func (s edgeEvaluateFailingAppendStore) AppendEvent(context.Context, edgecore.AgentActionEvent) (edgecore.AgentActionEvent, error) {
	return edgecore.AgentActionEvent{}, errors.New("append failed: Bearer edge-append-failure-secret")
}

func (c *edgeEvaluateStubSafetyClient) Evaluate(ctx context.Context, in *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, in)
	if c.err != nil {
		return nil, c.err
	}
	if c.response != nil {
		return c.response, nil
	}
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "allowed"}, nil
}

func (c *edgeEvaluatePolicySafetyClient) Evaluate(_ context.Context, in *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, in)
	c.mu.Unlock()
	return policybundles.EvaluatePolicyCheck(c.policy, c.snapshot, in), nil
}

func (c *edgeEvaluatePolicySafetyClient) capturedRequests() []*pb.PolicyCheckRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*pb.PolicyCheckRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

func (c *edgeEvaluatePolicySafetyClient) Check(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("unexpected Check call")
}

func (c *edgeEvaluatePolicySafetyClient) Explain(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("unexpected Explain call")
}

func (c *edgeEvaluatePolicySafetyClient) Simulate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("unexpected Simulate call")
}

func (c *edgeEvaluatePolicySafetyClient) ListSnapshots(context.Context, *pb.ListSnapshotsRequest, ...grpc.CallOption) (*pb.ListSnapshotsResponse, error) {
	return nil, errors.New("unexpected ListSnapshots call")
}

func loadEdgeEvaluatePolicySimulationFixtures(t *testing.T) map[string]edgeEvaluatePolicySimulationCase {
	t.Helper()
	path := filepath.Join("..", "..", "..", "examples", "cordum-edge-pack", "fixtures", "policy-simulations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edge policy simulation fixtures %s: %v", path, err)
	}
	var fixture edgeEvaluatePolicySimulationFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse edge policy simulation fixtures %s: %v", path, err)
	}
	out := make(map[string]edgeEvaluatePolicySimulationCase, len(fixture.Cases))
	for _, tc := range fixture.Cases {
		out[tc.Name] = tc
	}
	return out
}

func loadEdgeEvaluateDemoPolicy(t *testing.T) *config.SafetyPolicy {
	t.Helper()
	path := filepath.Join("..", "..", "..", "examples", "cordum-edge-pack", "overlays", "policy.fragment.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read demo Edge policy %s: %v", path, err)
	}
	policy, err := config.ParseSafetyPolicy(data)
	if err != nil {
		t.Fatalf("parse demo Edge policy %s: %v", path, err)
	}
	if policy == nil {
		t.Fatalf("parse demo Edge policy %s returned nil", path)
	}
	return policy
}

func edgeEvaluateResponseDecisionForPolicyDecision(decision string) string {
	if decision == "REQUIRE_HUMAN" {
		return string(edgecore.DecisionRequireApproval)
	}
	return decision
}

func edgeEvaluateEventDecisionForPolicyDecision(decision string) edgecore.EdgeDecision {
	switch decision {
	case "ALLOW":
		return edgecore.DecisionAllow
	case "DENY":
		return edgecore.DecisionDeny
	case "REQUIRE_HUMAN":
		return edgecore.DecisionRequireApproval
	default:
		return edgecore.EdgeDecision(decision)
	}
}

func (c *edgeEvaluateStubSafetyClient) Check(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("unexpected Check call")
}

func (c *edgeEvaluateStubSafetyClient) Explain(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("unexpected Explain call")
}

func (c *edgeEvaluateStubSafetyClient) Simulate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("unexpected Simulate call")
}

func (c *edgeEvaluateStubSafetyClient) ListSnapshots(context.Context, *pb.ListSnapshotsRequest, ...grpc.CallOption) (*pb.ListSnapshotsResponse, error) {
	return nil, errors.New("unexpected ListSnapshots call")
}
