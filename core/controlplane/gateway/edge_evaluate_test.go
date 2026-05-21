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
	"time"

	"github.com/cordum/cordum/core/audit"
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
	s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})

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
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing tenant status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}

	beforeBadJSON := edgeRedisKeySnapshot(t, s)
	badJSON := httptest.NewRequest(http.MethodPost, "/api/v1/edge/evaluate", strings.NewReader(`{"session_id":`))
	addEdgeRouteAuth(badJSON)
	badJSON.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, badJSON)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidJSON)
	assertBodyOmits(t, rr.Body.String(), "enterprise_hook_token", "Bearer")
	assertEdgeRedisKeysUnchanged(t, s, beforeBadJSON)

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

func TestEdgeEvaluateActionHashIsRiskTagOrderInvariant(t *testing.T) {
	base := edgecore.AgentActionEvent{
		TenantID:       "tenant-a",
		SessionID:      "sess-action-hash",
		ExecutionID:    "exec-action-hash",
		PrincipalID:    "principal-a",
		Layer:          edgecore.LayerHook,
		Kind:           edgecore.EventKindHookPreToolUse,
		ToolName:       "Bash",
		ToolUseID:      "toolu-action-hash",
		ActionName:     "bash.exec",
		Capability:     "exec.shell",
		RiskTags:       []string{"exec", "network", "destructive"},
		Labels:         edgecore.Labels{"zeta": "last", "alpha": "first"},
		InputHash:      "sha256:input-action-hash",
		PolicySnapshot: "policy-v1",
	}
	scrambled := base
	scrambled.RiskTags = []string{"destructive", "exec", "network"}
	scrambled.Labels = edgecore.Labels{"alpha": "first", "zeta": "last"}

	first, err := edgeEvaluateActionHash(base, "policy-v1")
	if err != nil {
		t.Fatalf("edgeEvaluateActionHash first: %v", err)
	}
	second, err := edgeEvaluateActionHash(scrambled, "policy-v1")
	if err != nil {
		t.Fatalf("edgeEvaluateActionHash scrambled: %v", err)
	}
	if first != second {
		t.Fatalf("action_hash changed with equivalent risk-tag/label ordering: first=%s second=%s", first, second)
	}
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
		wantCommand        string
		wantRedaction      string
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
					Budgets:        &pb.BudgetConstraints{MaxRuntimeMs: 5000},
					Toolchain:      &pb.ToolchainConstraints{AllowedCommands: []string{"npm test"}},
					RedactionLevel: "strict",
				},
			},
			wantDecision:    "CONSTRAIN",
			wantPermission:  "allow",
			wantExitCode:    0,
			wantConstraints: true,
			wantCommand:     "npm test",
			wantRedaction:   "strict",
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
			if tc.wantCommand != "" {
				assertEdgeConstraintListContains(t, resp.Constraints, "toolchain", "allowedCommands", tc.wantCommand)
			}
			if tc.wantRedaction != "" && resp.Constraints["redactionLevel"] != tc.wantRedaction {
				t.Fatalf("constraints.redactionLevel = %#v, want %q body=%s", resp.Constraints["redactionLevel"], tc.wantRedaction, rr.Body.String())
			}
			if tc.wantTerminalSubstr != "" && !strings.Contains(strings.ToLower(resp.TerminalMessage), tc.wantTerminalSubstr) {
				t.Fatalf("terminal_message = %q, want substring %q", resp.TerminalMessage, tc.wantTerminalSubstr)
			}
		})
	}
}

func assertEdgeConstraintListContains(t *testing.T, constraints map[string]any, section, field, want string) {
	t.Helper()
	nested, ok := constraints[section].(map[string]any)
	if !ok {
		t.Fatalf("constraints[%q] = %#v, want object", section, constraints[section])
	}
	values, ok := nested[field].([]any)
	if !ok {
		t.Fatalf("constraints[%q][%q] = %#v, want list", section, field, nested[field])
	}
	for _, value := range values {
		if got, ok := value.(string); ok && got == want {
			return
		}
	}
	t.Fatalf("constraints[%q][%q] = %#v, want entry %q", section, field, values, want)
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

// EDGE-059 — /api/v1/edge/evaluate must let callers shorten the default
// 5-minute approval TTL via approval_ttl_seconds in the request body.
// Pre-fix the TTL was hardcoded; e2e gates that needed to exercise
// approval expiration (EDGE-056 gate_approval_expired) had no way to
// shorten the wait without violating the bounded-sleep rail. Per
// architect-72ce direction (msg-2595e7ea), the override is shorten-only
// to preserve the security floor against malicious indefinite-hold
// attempts.

func edgeEvaluateBodyWithApprovalTTL(sessionID, executionID, tenantID, toolName string, input map[string]any, ttlSeconds int) string {
	body := map[string]any{
		"tenant_id":            tenantID,
		"principal_id":         "principal-edge-a",
		"session_id":           sessionID,
		"execution_id":         executionID,
		"agent_product":        "claude-code",
		"layer":                "hook",
		"kind":                 "hook.pre_tool_use",
		"tool_name":            toolName,
		"input_redacted":       input,
		"approval_ttl_seconds": ttlSeconds,
	}
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func setupEdgeEvaluateApprovalRequiredServer(t *testing.T) (*server, http.Handler, edgeSessionCreateResponseJSON) {
	t.Helper()
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "approval required",
		RuleId:           "edge059.test",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot
	return s, handler, session
}

func TestEnqueueEdgeEvaluateApprovalRespectsCustomTTL(t *testing.T) {
	s, handler, session := setupEdgeEvaluateApprovalRequiredServer(t)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate",
		edgeEvaluateBodyWithApprovalTTL(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}, 2))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if resp.Decision != string(edgecore.DecisionRequireApproval) || resp.ApprovalRef == "" {
		t.Fatalf("decision = %q, approval_ref = %q, want REQUIRE_APPROVAL + non-empty ref", resp.Decision, resp.ApprovalRef)
	}

	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, resp.ApprovalRef)
	if err != nil || !ok || stored == nil || stored.ExpiresAt == nil {
		t.Fatalf("GetApproval(%q) = (%#v,%v,%v), want stored approval with expires_at", resp.ApprovalRef, stored, ok, err)
	}
	now := time.Now().UTC()
	expiresIn := stored.ExpiresAt.Sub(now)
	// Expect ≈ 2s — allow [1s, 5s] slack for scheduling.
	if expiresIn < time.Second || expiresIn > 5*time.Second {
		t.Fatalf("approval_ttl_seconds=2 → expires_at delta = %v, want [1s, 5s] (now=%v expires=%v)", expiresIn, now, *stored.ExpiresAt)
	}
}

func TestEnqueueEdgeEvaluateApprovalDefaultTTLWhenUnset(t *testing.T) {
	s, handler, session := setupEdgeEvaluateApprovalRequiredServer(t)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate",
		edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, resp.ApprovalRef)
	if err != nil || !ok || stored == nil || stored.ExpiresAt == nil {
		t.Fatalf("GetApproval(%q): (%#v,%v,%v), want stored approval with expires_at", resp.ApprovalRef, stored, ok, err)
	}
	now := time.Now().UTC()
	expiresIn := stored.ExpiresAt.Sub(now)
	// Expect ≈ 5min — allow [4m55s, 5m05s] slack.
	if expiresIn < 295*time.Second || expiresIn > 305*time.Second {
		t.Fatalf("default TTL → expires_at delta = %v, want ~5min ±5s", expiresIn)
	}
}

func TestEnqueueEdgeEvaluateApprovalCapsExtendAttempts(t *testing.T) {
	s, handler, session := setupEdgeEvaluateApprovalRequiredServer(t)

	// Caller asks for 999 seconds (16+ minutes) — server-side cap drops to 5min default.
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate",
		edgeEvaluateBodyWithApprovalTTL(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}, 999))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, resp.ApprovalRef)
	if err != nil || !ok || stored == nil || stored.ExpiresAt == nil {
		t.Fatalf("GetApproval(%q): (%#v,%v,%v)", resp.ApprovalRef, stored, ok, err)
	}
	expiresIn := stored.ExpiresAt.Sub(time.Now().UTC())
	if expiresIn < 295*time.Second || expiresIn > 305*time.Second {
		t.Fatalf("approval_ttl_seconds=999 (>5min cap) → expires_at delta = %v, want ~5min ±5s (cap fired)", expiresIn)
	}
}

func TestEnqueueEdgeEvaluateApprovalEnforcesNegativeFallthroughToDefault(t *testing.T) {
	s, handler, session := setupEdgeEvaluateApprovalRequiredServer(t)

	// Negative value: handler treats as "use default".
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate",
		edgeEvaluateBodyWithApprovalTTL(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}, -5))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, resp.ApprovalRef)
	if err != nil || !ok || stored == nil || stored.ExpiresAt == nil {
		t.Fatalf("GetApproval(%q): (%#v,%v,%v)", resp.ApprovalRef, stored, ok, err)
	}
	expiresIn := stored.ExpiresAt.Sub(time.Now().UTC())
	if expiresIn < 295*time.Second || expiresIn > 305*time.Second {
		t.Fatalf("approval_ttl_seconds=-5 → expires_at delta = %v, want ~5min ±5s (negative falls through to default)", expiresIn)
	}
}

func TestGatewayEdgeEvaluateRetryConsumesApprovedApprovalAndDeniesDuplicate(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "rm -rf /var/edge-retry"}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("initial status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var initial edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &initial)
	if initial.Decision != string(edgecore.DecisionRequireApproval) || initial.ApprovalRef == "" {
		t.Fatalf("initial decision/ref = %q/%q, want REQUIRE_APPROVAL with ref body=%s", initial.Decision, initial.ApprovalRef, rr.Body.String())
	}

	if _, err := s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID:    edgeRouteTenant,
		ApprovalRef: initial.ApprovalRef,
		ResolverID:  "human-1",
		ResolvedBy:  "human-1",
		Reason:      "approved by reviewer",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("approve approval: %v", err)
	}

	retryBody := edgeEvaluateBodyWithApprovalRef(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd, initial.ApprovalRef)
	rr2 := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", retryBody)
	if rr2.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 body=%s", rr2.Code, rr2.Body.String())
	}
	var retry edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr2, &retry)
	if retry.Decision != string(edgecore.DecisionAllow) {
		t.Fatalf("retry decision = %q, want ALLOW body=%s", retry.Decision, rr2.Body.String())
	}
	if retry.PermissionDecision != "allow" || retry.ExitCode != 0 {
		t.Fatalf("retry permission/exit = %q/%d, want allow/0 body=%s", retry.PermissionDecision, retry.ExitCode, rr2.Body.String())
	}
	if retry.ApprovalRef != initial.ApprovalRef {
		t.Fatalf("retry approval_ref = %q, want %q", retry.ApprovalRef, initial.ApprovalRef)
	}
	if retry.WaitAfter != "" || retry.WaitStrategy != "" {
		t.Fatalf("retry should clear wait guidance, got wait_strategy=%q wait_after=%q", retry.WaitStrategy, retry.WaitAfter)
	}
	events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	var retryEvent *edgecore.AgentActionEvent
	for i := range events {
		if events[i].EventID == retry.EventID {
			retryEvent = &events[i]
			break
		}
	}
	if retryEvent == nil {
		t.Fatalf("retry event_id %q not found in persisted events: %#v", retry.EventID, events)
	}
	if retryEvent.Decision != edgecore.DecisionAllow || retryEvent.Status != edgecore.ActionStatusOK || retryEvent.ApprovalRef != initial.ApprovalRef {
		t.Fatalf("retry event decision/status/ref = %q/%q/%q, want ALLOW/ok/%q (event=%#v)",
			retryEvent.Decision, retryEvent.Status, retryEvent.ApprovalRef, initial.ApprovalRef, retryEvent)
	}

	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, initial.ApprovalRef)
	if err != nil || !ok || stored == nil {
		t.Fatalf("GetApproval after consume = (%#v, %v, %v); want stored", stored, ok, err)
	}
	if stored.ConsumedAt == nil {
		t.Fatalf("approval not marked consumed: %#v", stored)
	}

	rr3 := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", retryBody)
	if rr3.Code != http.StatusOK {
		t.Fatalf("duplicate retry status = %d, want 200 body=%s", rr3.Code, rr3.Body.String())
	}
	var dup edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr3, &dup)
	if dup.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("duplicate retry decision = %q, want DENY body=%s", dup.Decision, rr3.Body.String())
	}
	combined := strings.ToLower(dup.Reason + " " + dup.TerminalMessage)
	if !strings.Contains(combined, "consume") && !strings.Contains(combined, "request a new approval") {
		t.Fatalf("duplicate retry reason/terminal = %q/%q, want consume/new-approval hint", dup.Reason, dup.TerminalMessage)
	}
	if dup.WaitAfter != "request_new_approval" {
		t.Fatalf("duplicate wait_after = %q, want request_new_approval", dup.WaitAfter)
	}
}

func TestGatewayEdgeEvaluateRetryRejectsSelfApprovalCaller(t *testing.T) {
	for _, tc := range []struct {
		name        string
		explicitRef bool
	}{
		{name: "explicit_approval_ref", explicitRef: true},
		{name: "auto_consume_reusable_approval", explicitRef: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertEdgeEvaluateSelfApprovalRetryDenied(t, tc.name, tc.explicitRef)
		})
	}
}

func assertEdgeEvaluateSelfApprovalRetryDenied(t *testing.T, name string, explicitRef bool) {
	t.Helper()
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	sink := &testAuditSender{}
	s.auditExporter = sink
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "deploy risky change for " + name}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	var initial edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &initial)
	if initial.Decision != string(edgecore.DecisionRequireApproval) || initial.ApprovalRef == "" {
		t.Fatalf("initial decision/ref = %q/%q, want REQUIRE_APPROVAL + ref body=%s", initial.Decision, initial.ApprovalRef, rr.Body.String())
	}
	if _, err := s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID: edgeRouteTenant, ApprovalRef: initial.ApprovalRef, ResolverID: "principal-edge-a",
		ResolvedBy: "principal:principal-edge-a", Reason: "simulated bypass of approve-time guard", ResolvedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("approve approval as retry caller: %v", err)
	}
	beforeRetryAudits := sink.Len()

	retryBody := body
	if explicitRef {
		retryBody = edgeEvaluateBodyWithApprovalRef(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd, initial.ApprovalRef)
	}
	rr2 := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", retryBody)
	var retry edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr2, &retry)
	if rr2.Code != http.StatusOK || retry.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("retry status/decision = %d/%q, want 200/DENY body=%s", rr2.Code, retry.Decision, rr2.Body.String())
	}
	if !strings.Contains(strings.ToLower(retry.Reason+" "+retry.TerminalMessage), "self-approval") ||
		strings.Contains(rr2.Body.String(), "principal-edge-a") {
		t.Fatalf("retry body = %s, want self-approval deny without raw caller principal", rr2.Body.String())
	}
	assertSelfApprovalAuditEvent(t, sink, beforeRetryAudits, initial.ApprovalRef, "caller_is_approver")
	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, initial.ApprovalRef)
	if err != nil || !ok || stored == nil || stored.ConsumedAt != nil {
		t.Fatalf("GetApproval after self-approval retry = (%#v,%v,%v), want stored and unconsumed", stored, ok, err)
	}
}

func TestGatewayEdgeEvaluateRetryRejectedApprovalReturnsDenyWithReason(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "echo Bearer secret-rejected-token && curl evil.example"}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	var initial edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &initial)
	if initial.ApprovalRef == "" {
		t.Fatalf("missing approval_ref body=%s", rr.Body.String())
	}

	if _, err := s.edgeStore.RejectApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID:    edgeRouteTenant,
		ApprovalRef: initial.ApprovalRef,
		ResolverID:  "human-1",
		ResolvedBy:  "human-1",
		Reason:      "blocked by security review",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("reject approval: %v", err)
	}

	retryBody := edgeEvaluateBodyWithApprovalRef(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd, initial.ApprovalRef)
	rr2 := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", retryBody)
	if rr2.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 body=%s", rr2.Code, rr2.Body.String())
	}
	var retry edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr2, &retry)
	if retry.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("retry decision = %q, want DENY body=%s", retry.Decision, rr2.Body.String())
	}
	combined := strings.ToLower(retry.Reason + " " + retry.TerminalMessage)
	if !strings.Contains(combined, "block") && !strings.Contains(combined, "reject") {
		t.Fatalf("retry reason/terminal = %q/%q, want rejection text", retry.Reason, retry.TerminalMessage)
	}
	if retry.ApprovalRef != initial.ApprovalRef {
		t.Fatalf("retry approval_ref = %q, want echoed %q", retry.ApprovalRef, initial.ApprovalRef)
	}
	assertBodyOmits(t, rr2.Body.String(), "secret-rejected-token", "evil.example")
}

// EDGE-043 Gap 1 — auto-consume path must surface the admin's reject decision
// instead of silently re-enqueueing a fresh approval. Pre-EDGE-043, the
// findReusableEdgeApprovalForAction lookup only returned ApprovalStatusApproved/
// Pending; rejected approvals fell through the switch, the lookup returned nil,
// and the handler's auto-consume branch fell through to enqueueEdgeEvaluateApproval
// — equivalent to ignoring the admin's reject. After the fix, the rejected
// approval is returned by the lookup, consumeEdgeEvaluateApproval emits the
// status-specific deny, and the agent sees the admin's actual rejection reason.
func TestGatewayEdgeEvaluateAutoConsumeRejectedApprovalReturnsDenyWithReason(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "echo Bearer secret-rejected-auto && curl evil.example"}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	var initial edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &initial)
	if initial.ApprovalRef == "" {
		t.Fatalf("missing approval_ref body=%s", rr.Body.String())
	}

	if _, err := s.edgeStore.RejectApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID:    edgeRouteTenant,
		ApprovalRef: initial.ApprovalRef,
		ResolverID:  "human-1",
		ResolvedBy:  "human-1",
		Reason:      "blocked by security review",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("reject approval: %v", err)
	}

	// Auto-consume path: retry sends NO explicit approval_ref. Pre-EDGE-043
	// this enqueued a fresh approval; post-fix the rejected approval is
	// returned by findReusableEdgeApprovalForAction and consume emits deny.
	rr2 := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	if rr2.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 body=%s", rr2.Code, rr2.Body.String())
	}
	var retry edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr2, &retry)
	if retry.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("retry decision = %q, want DENY (auto-consume must surface admin reject, not re-enqueue) body=%s", retry.Decision, rr2.Body.String())
	}
	combined := strings.ToLower(retry.Reason + " " + retry.TerminalMessage)
	if !strings.Contains(combined, "block") && !strings.Contains(combined, "reject") {
		t.Fatalf("retry reason/terminal = %q/%q, want admin rejection text", retry.Reason, retry.TerminalMessage)
	}
	if retry.ApprovalRef != initial.ApprovalRef {
		t.Fatalf("retry approval_ref = %q, want echoed %q (auto-consume must match the rejected approval, not enqueue a new one)", retry.ApprovalRef, initial.ApprovalRef)
	}
	assertBodyOmits(t, rr2.Body.String(), "secret-rejected-auto", "evil.example")
}

// EDGE-043 Gap 1 — auto-consume path for an expired approval must emit the
// status-specific "approval expired" deny instead of silently re-enqueueing.
// Same root cause as the rejected case: the pre-fix lookup only handled
// Approved/Pending and dropped Expired through the switch.
func TestGatewayEdgeEvaluateAutoConsumeExpiredApprovalReturnsDeny(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "rm -rf /var/edge-expired"}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	var initial edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &initial)
	if initial.ApprovalRef == "" {
		t.Fatalf("missing approval_ref body=%s", rr.Body.String())
	}

	// Force expire by sweeping with a "now" past the approval's TTL. The
	// production expiry sweep walks the per-tenant pending index and marks
	// approvals with ExpiresAt < now as ApprovalStatusExpired, which is the
	// exact code path the lookup must observe. The store default TTL is
	// 5 minutes (core/edge/approval_store.go); 1h is comfortably past that.
	expiredAt := time.Now().UTC().Add(time.Hour)
	if _, err := s.edgeStore.ExpireApprovals(context.Background(), edgeRouteTenant, expiredAt); err != nil {
		t.Fatalf("ExpireApprovals: %v", err)
	}
	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, initial.ApprovalRef)
	if err != nil || !ok || stored == nil || stored.Status != edgecore.ApprovalStatusExpired {
		t.Fatalf("GetApproval after expire = (%#v, %v, %v); want expired", stored, ok, err)
	}

	// Auto-consume path: retry without explicit approval_ref. Pre-fix this
	// re-enqueued; post-fix the expired approval is returned by the lookup
	// and consume emits "approval expired" deny.
	rr2 := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	if rr2.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 body=%s", rr2.Code, rr2.Body.String())
	}
	var retry edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr2, &retry)
	if retry.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("retry decision = %q, want DENY (auto-consume must surface expired status, not re-enqueue) body=%s", retry.Decision, rr2.Body.String())
	}
	combined := strings.ToLower(retry.Reason + " " + retry.TerminalMessage)
	if !strings.Contains(combined, "expired") {
		t.Fatalf("retry reason/terminal = %q/%q, want expired hint", retry.Reason, retry.TerminalMessage)
	}
	if retry.ApprovalRef != initial.ApprovalRef {
		t.Fatalf("retry approval_ref = %q, want echoed %q (auto-consume must match the expired approval, not enqueue a new one)", retry.ApprovalRef, initial.ApprovalRef)
	}
}

func TestGatewayEdgeEvaluateRetryChangedCommandDeniesWithoutConsuming(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	approvedCmd := map[string]any{"command": "rm -rf /var/edge-approved"}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", approvedCmd)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	var initial edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &initial)
	if initial.ApprovalRef == "" {
		t.Fatalf("missing approval_ref body=%s", rr.Body.String())
	}
	if _, err := s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID:    edgeRouteTenant,
		ApprovalRef: initial.ApprovalRef,
		ResolverID:  "human-1",
		ResolvedBy:  "human-1",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	mutatedCmd := map[string]any{"command": "rm -rf /etc/secrets-different"}
	retryBody := edgeEvaluateBodyWithApprovalRef(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", mutatedCmd, initial.ApprovalRef)
	rr2 := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", retryBody)
	if rr2.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 body=%s", rr2.Code, rr2.Body.String())
	}
	var retry edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr2, &retry)
	if retry.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("retry decision = %q, want DENY body=%s", retry.Decision, rr2.Body.String())
	}
	combined := strings.ToLower(retry.Reason + " " + retry.TerminalMessage)
	if !strings.Contains(combined, "mismatch") {
		t.Fatalf("retry reason/terminal = %q/%q, want mismatch hint", retry.Reason, retry.TerminalMessage)
	}

	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, initial.ApprovalRef)
	if err != nil || !ok || stored == nil {
		t.Fatalf("GetApproval = (%#v, %v, %v); want stored", stored, ok, err)
	}
	if stored.ConsumedAt != nil {
		t.Fatalf("approval was consumed despite changed command: %#v", stored)
	}
	if stored.Status != edgecore.ApprovalStatusApproved {
		t.Fatalf("approval status = %q, want approved (still claimable for valid retry)", stored.Status)
	}
}

func TestGatewayEdgeEvaluateRetryStalePolicySnapshotDeniesWithoutConsuming(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	// Initial enqueue requires the approval's policy_snapshot to match the
	// session's; align it here. The mutation to a different snapshot below
	// simulates a policy refresh between approval and retry.
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "rm -rf /var/edge-snapshot"}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	var initial edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &initial)
	if initial.ApprovalRef == "" {
		t.Fatalf("missing approval_ref body=%s", rr.Body.String())
	}
	if initial.PolicySnapshot != session.PolicySnapshot {
		t.Fatalf("initial policy_snapshot = %q, want %q", initial.PolicySnapshot, session.PolicySnapshot)
	}
	if _, err := s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID:    edgeRouteTenant,
		ApprovalRef: initial.ApprovalRef,
		ResolverID:  "human-1",
		ResolvedBy:  "human-1",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	safety.mu.Lock()
	safety.response = &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "still needs approval (policy refreshed)",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
		PolicySnapshot:   "snap-stale-B-after-refresh",
	}
	safety.mu.Unlock()

	retryBody := edgeEvaluateBodyWithApprovalRef(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd, initial.ApprovalRef)
	rr2 := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", retryBody)
	if rr2.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 body=%s", rr2.Code, rr2.Body.String())
	}
	var retry edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr2, &retry)
	if retry.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("retry decision = %q, want DENY body=%s", retry.Decision, rr2.Body.String())
	}
	combined := strings.ToLower(retry.Reason + " " + retry.TerminalMessage)
	if !strings.Contains(combined, "mismatch") {
		t.Fatalf("retry reason/terminal = %q/%q, want mismatch hint", retry.Reason, retry.TerminalMessage)
	}

	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, initial.ApprovalRef)
	if err != nil || !ok || stored == nil {
		t.Fatalf("GetApproval = (%#v, %v, %v); want stored", stored, ok, err)
	}
	if stored.ConsumedAt != nil {
		t.Fatalf("approval was consumed despite stale snapshot: %#v", stored)
	}
}

func TestGatewayEdgeEvaluateRetryConcurrentExactlyOneAllow(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "rm -rf /var/edge-race"}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	var initial edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &initial)
	if initial.ApprovalRef == "" {
		t.Fatalf("missing approval_ref body=%s", rr.Body.String())
	}
	if _, err := s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID:    edgeRouteTenant,
		ApprovalRef: initial.ApprovalRef,
		ResolverID:  "human-1",
		ResolvedBy:  "human-1",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	retryBody := edgeEvaluateBodyWithApprovalRef(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd, initial.ApprovalRef)

	// Architect's spec is "two concurrent retries against one approved ref produce
	// exactly one ALLOW". The safety invariant is consume-once: among parallel
	// retries the ClaimApproval CAS lets at most one through. A small stagger
	// keeps the per-execution AppendEvent WATCH/MULTI dance from contending past
	// its retry budget, but the consume CAS still races (the second goroutine
	// reads the still-pending-consume approval before the first's CAS commits).
	const N = 2
	decisions := make([]string, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i) * 5 * time.Millisecond)
			rrr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", retryBody)
			if rrr.Code != http.StatusOK {
				decisions[i] = "STATUS_" + strconv.Itoa(rrr.Code)
				return
			}
			var resp edgeEvaluateResponseJSON
			if err := json.Unmarshal(rrr.Body.Bytes(), &resp); err != nil {
				decisions[i] = "DECODE_ERR"
				return
			}
			decisions[i] = resp.Decision
		}()
	}
	wg.Wait()

	allowCount := 0
	for _, d := range decisions {
		if d == string(edgecore.DecisionAllow) {
			allowCount++
		}
	}
	// Consume-once is the safety property: never more than one ALLOW. Other
	// outcomes may be DENY (CAS lost) or a transient 5xx (event-append WATCH
	// retry exhaustion under contention) — both are acceptable as long as the
	// approval was not double-consumed.
	if allowCount != 1 {
		t.Fatalf("concurrent retries got %d ALLOW (consume-once requires exactly 1): decisions=%v", allowCount, decisions)
	}

	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, initial.ApprovalRef)
	if err != nil || !ok || stored == nil || stored.ConsumedAt == nil {
		t.Fatalf("approval not consumed after race: stored=%#v ok=%v err=%v", stored, ok, err)
	}
}

func TestGatewayEdgeEvaluateInlineWaitDefaultDoesNotBlock(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	_, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "rm -rf /var/edge-no-wait"}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd)

	start := time.Now()
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if resp.Decision != string(edgecore.DecisionRequireApproval) {
		t.Fatalf("decision = %q, want REQUIRE_APPROVAL (default returns immediately) body=%s", resp.Decision, rr.Body.String())
	}
	if resp.ApprovalRef == "" {
		t.Fatalf("approval_ref empty body=%s", rr.Body.String())
	}
	// Default (no wait_for_approval) must respond well under the inline-wait
	// poll interval, never anywhere near a default 30s timeout.
	if elapsed > 1*time.Second {
		t.Fatalf("default response took %v, want sub-second (no inline wait when wait_for_approval=false)", elapsed)
	}
}

func TestGatewayEdgeEvaluateInlineWaitApproveDuringWaitReturnsAllow(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "rm -rf /var/edge-wait-approve"}
	body := edgeEvaluateBodyWithInlineWait(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd, "", 3000)

	// Approver runs concurrently with the inline-wait handler: it can't fire
	// until the handler has enqueued the approval (which happens just before
	// the wait loop), so it polls the store briefly until the approval exists.
	approverDone := make(chan struct{})
	go func() {
		defer close(approverDone)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		var ref string
		for {
			page, err := s.edgeStore.ListApprovals(ctx, edgecore.ListApprovalsQuery{TenantID: edgeRouteTenant, Limit: 10})
			if err == nil {
				for _, a := range page.Items {
					if a.SessionID == session.SessionID && a.ExecutionID == session.ExecutionID && a.Status == edgecore.ApprovalStatusPending {
						ref = a.ApprovalRef
						break
					}
				}
			}
			if ref != "" {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
		_, _ = s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
			TenantID:    edgeRouteTenant,
			ApprovalRef: ref,
			ResolverID:  "human-1",
			ResolvedBy:  "human-1",
			ResolvedAt:  time.Now().UTC(),
		})
	}()

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	<-approverDone
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if resp.Decision != string(edgecore.DecisionAllow) {
		t.Fatalf("decision = %q, want ALLOW (approved during inline wait) body=%s", resp.Decision, rr.Body.String())
	}
	if resp.PermissionDecision != "allow" || resp.ExitCode != 0 {
		t.Fatalf("permission/exit = %q/%d, want allow/0", resp.PermissionDecision, resp.ExitCode)
	}
	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, resp.ApprovalRef)
	if err != nil || !ok || stored == nil || stored.ConsumedAt == nil {
		t.Fatalf("approval not consumed after inline-wait approve: stored=%#v ok=%v err=%v", stored, ok, err)
	}
}

func TestGatewayEdgeEvaluateInlineWaitRejectDuringWaitReturnsDeny(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "echo Bearer wait-reject-secret"}
	body := edgeEvaluateBodyWithInlineWait(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd, "", 3000)

	rejecterDone := make(chan struct{})
	go func() {
		defer close(rejecterDone)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		var ref string
		for {
			page, err := s.edgeStore.ListApprovals(ctx, edgecore.ListApprovalsQuery{TenantID: edgeRouteTenant, Limit: 10})
			if err == nil {
				for _, a := range page.Items {
					if a.SessionID == session.SessionID && a.ExecutionID == session.ExecutionID && a.Status == edgecore.ApprovalStatusPending {
						ref = a.ApprovalRef
						break
					}
				}
			}
			if ref != "" {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
		_, _ = s.edgeStore.RejectApproval(context.Background(), edgecore.ApprovalResolution{
			TenantID:    edgeRouteTenant,
			ApprovalRef: ref,
			ResolverID:  "human-1",
			ResolvedBy:  "human-1",
			Reason:      "blocked during wait",
			ResolvedAt:  time.Now().UTC(),
		})
	}()

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	<-rejecterDone
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if resp.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("decision = %q, want DENY body=%s", resp.Decision, rr.Body.String())
	}
	combined := strings.ToLower(resp.Reason + " " + resp.TerminalMessage)
	if !strings.Contains(combined, "block") && !strings.Contains(combined, "reject") {
		t.Fatalf("reason/terminal = %q/%q, want rejection text", resp.Reason, resp.TerminalMessage)
	}
	assertBodyOmits(t, rr.Body.String(), "wait-reject-secret")
}

func TestGatewayEdgeEvaluateInlineWaitTimeoutKeepsApprovalPending(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "needs approval",
		RuleId:           "approval-rule",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	cmd := map[string]any{"command": "rm -rf /var/edge-wait-timeout"}
	body := edgeEvaluateBodyWithInlineWait(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd, "", 400)

	start := time.Now()
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if resp.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("decision = %q, want DENY after inline-wait timeout body=%s", resp.Decision, rr.Body.String())
	}
	if resp.ApprovalRef == "" {
		t.Fatalf("approval_ref empty body=%s", rr.Body.String())
	}
	if resp.WaitAfter != "approve_then_retry" {
		t.Fatalf("wait_after = %q, want approve_then_retry", resp.WaitAfter)
	}
	combined := strings.ToLower(resp.Reason + " " + resp.TerminalMessage + " " + resp.PermissionDecisionReason)
	if !strings.Contains(combined, "timeout") || !strings.Contains(combined, "retry") {
		t.Fatalf("timeout response reason/terminal/permission = %q/%q/%q, want timeout + retry guidance", resp.Reason, resp.TerminalMessage, resp.PermissionDecisionReason)
	}
	events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	var timeoutEvent *edgecore.AgentActionEvent
	for i := range events {
		if events[i].EventID == resp.EventID {
			timeoutEvent = &events[i]
			break
		}
	}
	if timeoutEvent == nil {
		t.Fatalf("timeout response event_id %q not found in persisted events: %#v", resp.EventID, events)
	}
	if timeoutEvent.Decision != edgecore.DecisionDeny || timeoutEvent.Status != edgecore.ActionStatusBlocked || timeoutEvent.ApprovalRef != resp.ApprovalRef {
		t.Fatalf("timeout event decision/status/ref = %q/%q/%q, want DENY/blocked/%q (event=%#v)",
			timeoutEvent.Decision, timeoutEvent.Status, timeoutEvent.ApprovalRef, resp.ApprovalRef, timeoutEvent)
	}
	if elapsed < 350*time.Millisecond {
		t.Fatalf("inline wait elapsed %v, expected >= ~400ms timeout", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("inline wait elapsed %v, expected to honor 400ms cap", elapsed)
	}

	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, resp.ApprovalRef)
	if err != nil || !ok || stored == nil {
		t.Fatalf("GetApproval = (%#v, %v, %v); want stored", stored, ok, err)
	}
	if stored.Status != edgecore.ApprovalStatusPending {
		t.Fatalf("approval status = %q, want pending after timeout", stored.Status)
	}
	if stored.ConsumedAt != nil {
		t.Fatalf("approval was consumed despite timeout: %#v", stored)
	}
}

func TestBoundEdgeEvaluateWaitTimeoutClampsAndDefaults(t *testing.T) {
	cases := []struct {
		name   string
		input  int
		wantMS int64
	}{
		{"zero defaults", 0, edgeEvaluateInlineWaitDefaultTimeoutMS},
		{"negative defaults", -100, edgeEvaluateInlineWaitDefaultTimeoutMS},
		{"under cap unchanged", 1500, 1500},
		{"at cap unchanged", int(edgeEvaluateInlineWaitMaxTimeout / time.Millisecond), int64(edgeEvaluateInlineWaitMaxTimeout / time.Millisecond)},
		{"over cap clamps", 60 * 60 * 1000, int64(edgeEvaluateInlineWaitMaxTimeout / time.Millisecond)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := boundEdgeEvaluateWaitTimeout(tc.input)
			if got.Milliseconds() != tc.wantMS {
				t.Fatalf("boundEdgeEvaluateWaitTimeout(%d) = %v, want %dms", tc.input, got, tc.wantMS)
			}
		})
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

// Gateway-side Safety Kernel outage behavior is intentionally duplicated only
// for persisted event evidence. Agentd hook fail-mode contract coverage lives in
// core/edge/agentd/fail_modes_test.go.
func TestGatewayEdgeEvaluateSafetyUnavailableVariantsPersistNoFalseAllow(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		err                  error
		policyMode           edgecore.PolicyMode
		command              string
		wantResponseDecision string
		wantPermission       string
		wantEventDecision    edgecore.EdgeDecision
		forbidden            []string
	}{
		{
			name:                 "timeout observe allows hook but records degraded not allow",
			err:                  context.DeadlineExceeded,
			policyMode:           edgecore.PolicyModeObserve,
			command:              "npm test ./core/edge",
			wantResponseDecision: string(edgecore.DecisionAllow),
			wantPermission:       "allow",
			wantEventDecision:    edgecore.DecisionRecorded,
		},
		{
			name:                 "connection error enterprise strict fails closed",
			err:                  errors.New("connect safety kernel: connection refused: Bearer fake.jwt.value"),
			policyMode:           edgecore.PolicyModeEnterpriseStrict,
			command:              "npm test ./core/edge",
			wantResponseDecision: string(edgecore.DecisionDeny),
			wantPermission:       "deny",
			wantEventDecision:    edgecore.DecisionDeny,
			forbidden:            []string{"Bearer fake.jwt.value"},
		},
		{
			name:                 "malformed response enforce high risk fails closed",
			err:                  errors.New("malformed safety response body: invalid character '<' near ghp_FAKETOKEN0000"),
			policyMode:           edgecore.PolicyModeEnforce,
			command:              "rm -rf ./tmp/edge-malformed-response",
			wantResponseDecision: string(edgecore.DecisionDeny),
			wantPermission:       "deny",
			wantEventDecision:    edgecore.DecisionDeny,
			forbidden:            []string{"ghp_FAKETOKEN0000"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{err: tc.err})
			session := createEdgeEvaluateSessionWithPolicyMode(t, handler, tc.policyMode)
			before := edgeRedisKeySnapshot(t, s)

			rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": tc.command}))
			if rr.Code != http.StatusOK {
				t.Fatalf("evaluate status = %d, want hook-friendly 200 body=%s", rr.Code, rr.Body.String())
			}
			mustNotContain(t, rr.Body.String(), tc.forbidden...)
			var resp edgeEvaluateResponseJSON
			decodeEdgeRouteJSON(t, rr, &resp)
			if resp.Decision != tc.wantResponseDecision || resp.PermissionDecision != tc.wantPermission || !resp.Degraded || resp.ErrorCode != "safety_unavailable" {
				t.Fatalf("response = decision:%q permission:%q degraded:%v error:%q, want %q/%q/true/safety_unavailable body=%s",
					resp.Decision, resp.PermissionDecision, resp.Degraded, resp.ErrorCode, tc.wantResponseDecision, tc.wantPermission, rr.Body.String())
			}
			if resp.EventID == "" {
				t.Fatalf("response missing event_id body=%s", rr.Body.String())
			}
			if after := edgeRedisKeySnapshot(t, s); after == before {
				t.Fatalf("edge Redis key snapshot did not change; want degraded event persisted")
			}

			events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
			if len(events) != 1 {
				t.Fatalf("persisted events = %d, want exactly one degraded event: %#v", len(events), events)
			}
			event := events[0]
			if event.EventID != resp.EventID || event.Kind != edgecore.EventKindPolicyDegraded || event.Status != edgecore.ActionStatusDegraded {
				t.Fatalf("event id/kind/status = %q/%q/%q, want response id %q/%q/degraded (event=%#v)",
					event.EventID, event.Kind, event.Status, resp.EventID, edgecore.EventKindPolicyDegraded, event)
			}
			if event.Decision != tc.wantEventDecision || event.Decision == edgecore.DecisionAllow {
				t.Fatalf("persisted degraded event decision = %q, want %q and never ALLOW", event.Decision, tc.wantEventDecision)
			}
			if event.ErrorCode != "safety_unavailable" || strings.Contains(event.ErrorMessage, "fake.jwt.value") || strings.Contains(event.ErrorMessage, "ghp_FAKETOKEN0000") {
				t.Fatalf("event error fields = %q/%q, want sanitized safety_unavailable", event.ErrorCode, event.ErrorMessage)
			}
		})
	}
}

func TestGatewayEdgeEvaluateUnknownDecisionEnumFailsClosedWithoutFalseAllow(t *testing.T) {
	s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:       pb.DecisionType(999),
		Reason:         "future enum from Safety Kernel with sk-test-fake-secret-xyz",
		RuleId:         "edge028.future-enum",
		PolicySnapshot: "snap-edge028-future-enum",
	}})
	session := createEdgeRouteSession(t, handler)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test ./core/edge"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("future enum evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	mustNotContain(t, rr.Body.String(), "sk-test-fake-secret-xyz")
	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if resp.Decision != string(edgecore.DecisionDeny) || resp.PermissionDecision != "deny" || resp.Degraded {
		t.Fatalf("future enum response = decision:%q permission:%q degraded:%v, want DENY/deny/not degraded body=%s", resp.Decision, resp.PermissionDecision, resp.Degraded, rr.Body.String())
	}
	if !bodyHasRedactionMarker(rr.Body.String()) {
		t.Fatalf("future enum response missing redacted reason marker: %s", rr.Body.String())
	}

	events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	if len(events) != 1 {
		t.Fatalf("future enum events = %d, want one deny event: %#v", len(events), events)
	}
	event := events[0]
	if event.Decision != edgecore.DecisionDeny || event.Decision == edgecore.DecisionAllow || event.Status != edgecore.ActionStatusBlocked || event.Kind != edgecore.EventKindHookPolicyDecision {
		t.Fatalf("future enum event decision/status/kind = %q/%q/%q, want DENY/blocked/policy_decision and never ALLOW", event.Decision, event.Status, event.Kind)
	}
	if event.DecisionReason != "<redacted>" || event.RuleID != "edge028.future-enum" || event.PolicySnapshot != "snap-edge028-future-enum" {
		t.Fatalf("future enum event policy fields = reason:%q rule:%q snapshot:%q", event.DecisionReason, event.RuleID, event.PolicySnapshot)
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
	beforeRejects := edgeRedisKeySnapshot(t, s)

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
	assertEdgeRedisKeysUnchanged(t, s, beforeRejects)
	if events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID); len(events) != 0 {
		t.Fatalf("raw payload persisted events = %#v, want none", events)
	}

	oversizeSentinel := "edge028-evaluate-oversize-secret"
	oversizeValue := oversizeSentinel + strings.Repeat("x", edgecore.MaxInputRedactedBytes+1024)
	oversize := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": oversizeValue}))
	if oversize.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize input status = %d, want 413 body=%s", oversize.Code, oversize.Body.String())
	}
	assertBodyOmits(t, oversize.Body.String(), oversizeSentinel)
	assertEdgeRedisKeysUnchanged(t, s, beforeRejects)
	if events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID); len(events) != 0 {
		t.Fatalf("oversize payload persisted events = %#v, want none", events)
	}
}

func TestGatewayEdgeEvaluateRejectsBodyOverMaxBytesWithoutOrphanKeys(t *testing.T) {
	s, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}})
	session := createEdgeRouteSession(t, handler)
	t.Setenv(envGatewayMaxJSONBodyBytes, "256")
	beforeOversizeBody := edgeRedisKeySnapshot(t, s)

	oversizeBodySentinel := "edge028-evaluate-max-body-secret"
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{
		"command": oversizeBodySentinel + strings.Repeat("x", 512),
	})
	if len(body) <= 256 {
		t.Fatalf("oversize fixture length = %d, want > 256", len(body))
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/evaluate", strings.NewReader(body))
	addEdgeRouteAuth(req)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("body over max bytes status = %d, want 403 tier-limit body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "tier_limit_exceeded") || !strings.Contains(rr.Body.String(), "max_body_bytes") {
		t.Fatalf("body over max bytes response = %s, want tier_limit_exceeded/max_body_bytes", rr.Body.String())
	}
	assertBodyOmits(t, rr.Body.String(), oversizeBodySentinel)
	assertEdgeRedisKeysUnchanged(t, s, beforeOversizeBody)
	if events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID); len(events) != 0 {
		t.Fatalf("body over max bytes persisted events = %#v, want none", events)
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

func TestEdgeSessionPolicyOverrideAttachment(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{
		response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "allowed"},
	}
	_, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeEvaluateSessionWithPolicyMode(t, handler, edgecore.PolicyModeObserve)
	want := edgecore.SessionPolicyAttachmentID(session.SessionID)
	if got := session.Session.Labels[edgecore.LabelPolicyAttachmentID]; got != want {
		t.Fatalf("session attachment label = %q, want %q in %#v", got, want, session.Session.Labels)
	}
	if got := session.Execution.Labels[edgecore.LabelPolicyAttachmentID]; got != want {
		t.Fatalf("execution attachment label = %q, want %q in %#v", got, want, session.Execution.Labels)
	}

	body, err := json.Marshal(map[string]any{
		"tenant_id":     edgeRouteTenant,
		"principal_id":  "principal-edge-a",
		"session_id":    session.SessionID,
		"execution_id":  session.ExecutionID,
		"agent_product": "claude-code",
		"layer":         "hook",
		"kind":          "hook.pre_tool_use",
		"tool_name":     "Bash",
		"input_redacted": map[string]any{
			"command": "npm test",
		},
		"labels": map[string]string{
			edgecore.LabelPolicyAttachmentID: "session/evil/policy",
		},
	})
	if err != nil {
		t.Fatalf("marshal evaluate body: %v", err)
	}
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", string(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	requests := safety.capturedRequests()
	if len(requests) != 1 {
		t.Fatalf("captured safety requests = %d, want 1", len(requests))
	}
	if got := requests[0].GetLabels()[edgecore.LabelPolicyAttachmentID]; got != want {
		t.Fatalf("policy request attachment label = %q, want %q in %#v", got, want, requests[0].GetLabels())
	}
}

func TestJobAttachmentCleanupOnEnd(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{}
	_, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeEvaluateSessionWithPolicyMode(t, handler, edgecore.PolicyModeObserve)
	if got := session.Session.Labels[edgecore.LabelPolicyAttachmentID]; got == "" {
		t.Fatalf("session did not receive policy attachment label: %#v", session.Session.Labels)
	}

	end := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/end", `{"status":"ended"}`)
	if end.Code != http.StatusOK {
		t.Fatalf("end session status = %d, want 200 body=%s", end.Code, end.Body.String())
	}
	evaluate := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate",
		edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}))
	if evaluate.Code != http.StatusConflict {
		t.Fatalf("evaluate after end status = %d, want 409 body=%s", evaluate.Code, evaluate.Body.String())
	}
	if requests := safety.capturedRequests(); len(requests) != 0 {
		t.Fatalf("terminal session still reached safety kernel with %d request(s)", len(requests))
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

func edgeEvaluateBodyWithApprovalRef(sessionID, executionID, tenantID, toolName string, input map[string]any, approvalRef string) string {
	body := map[string]any{
		"tenant_id":      tenantID,
		"principal_id":   "principal-edge-a",
		"session_id":     sessionID,
		"execution_id":   executionID,
		"agent_product":  "claude-code",
		"layer":          "hook",
		"kind":           "hook.pre_tool_use",
		"tool_name":      toolName,
		"input_redacted": input,
		"approval_ref":   approvalRef,
	}
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func edgeEvaluateBodyWithInlineWait(sessionID, executionID, tenantID, toolName string, input map[string]any, approvalRef string, timeoutMS int) string {
	body := map[string]any{
		"tenant_id":                tenantID,
		"principal_id":             "principal-edge-a",
		"session_id":               sessionID,
		"execution_id":             executionID,
		"agent_product":            "claude-code",
		"layer":                    "hook",
		"kind":                     "hook.pre_tool_use",
		"tool_name":                toolName,
		"input_redacted":           input,
		"wait_for_approval":        true,
		"approval_wait_timeout_ms": timeoutMS,
	}
	if approvalRef != "" {
		body["approval_ref"] = approvalRef
	}
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(data)
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

func (c *edgeEvaluateStubSafetyClient) capturedRequests() []*pb.PolicyCheckRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*pb.PolicyCheckRequest, len(c.requests))
	copy(out, c.requests)
	return out
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

// TestGatewayEdgeEvaluateEmitsAuditEventPerDecision pins EDGE-014 step-10
// Gateway audit instrumentation for the evaluate handler. Each persisted
// decision event must produce exactly one audit event of the matching
// edge.* type; raw command/prompt/secret content from the request body
// must NEVER appear in any Extra value (the SIEMEvent builder bounds
// every promoted field, but pin the contract end-to-end).
func TestGatewayEdgeEvaluateEmitsAuditEventPerDecision(t *testing.T) {
	cases := []struct {
		name       string
		decision   pb.DecisionType
		wantType   string
		wantSev    string
		wantPolicy edgecore.EdgeDecision
	}{
		{"allow_emits_policy_decision_info", pb.DecisionType_DECISION_TYPE_ALLOW, audit.EventEdgePolicyDecision, audit.SeverityInfo, edgecore.DecisionAllow},
		{"deny_emits_action_denied_high", pb.DecisionType_DECISION_TYPE_DENY, audit.EventEdgeActionDenied, audit.SeverityHigh, edgecore.DecisionDeny},
		// REQUIRE_HUMAN path requires extra approval-store fixture
		// (valid principal_id, rule, and policy snapshot) before it
		// reaches the decision audit emission point. Coverage for
		// approval_requested audit emission lives in the dedicated
		// approval-flow test once that slice lands.
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
				Decision: c.decision,
				Reason:   "test reason",
			}}
			s, handler := newEdgeEvaluateTestServer(t, stub)
			sink := &testAuditSender{}
			s.auditExporter = sink
			session := createEdgeRouteSession(t, handler)
			// The session create itself emits 2 events (session_started +
			// execution_started); reset by reading then capturing length.
			before := sink.Len()

			rec := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(
				session.SessionID, session.ExecutionID, edgeRouteTenant,
				"Bash", map[string]any{"command": "Authorization: Bearer leaky-evaluate-secret"}))
			if rec.Code != http.StatusOK {
				t.Fatalf("evaluate status = %d body=%s", rec.Code, rec.Body.String())
			}

			after := sink.Len()
			if after-before != 1 {
				t.Fatalf("evaluate audit events emitted = %d, want 1", after-before)
			}
			ev := sink.Get(after - 1)
			if ev.EventType != c.wantType {
				t.Errorf("EventType = %q, want %q", ev.EventType, c.wantType)
			}
			if ev.Severity != c.wantSev {
				t.Errorf("Severity = %q, want %q", ev.Severity, c.wantSev)
			}
			if ev.TenantID != edgeRouteTenant {
				t.Errorf("TenantID = %q, want %q", ev.TenantID, edgeRouteTenant)
			}
			// Raw command must NOT leak into Extra.
			for k, v := range ev.Extra {
				if strings.Contains(v, "Authorization") || strings.Contains(v, "Bearer") || strings.Contains(v, "leaky-evaluate-secret") {
					t.Errorf("Extra[%q] leaked secret: %q", k, v)
				}
			}
		})
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
