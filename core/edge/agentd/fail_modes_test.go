package agentd

import (
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

func TestApplyFailModeMatrixCoversObserveEnforceEnterpriseStrict(t *testing.T) {
	cases := []struct {
		name         string
		ctx          FailModeContext
		wantDecision claude.Decision
		wantDegraded bool
		wantFailed   bool
		wantTerminal bool
	}{
		{
			name:         "observe + gateway unavailable + safe action -> allow degraded",
			ctx:          FailModeContext{PolicyMode: edgecore.PolicyModeObserve, ActionIsKnownSafe: true, GatewayErrorCategory: GatewayErrorUnavailable},
			wantDecision: claude.DecisionAllow,
			wantDegraded: true,
			wantFailed:   false,
			wantTerminal: false,
		},
		{
			name:         "observe + gateway unavailable + risky action -> still allow (observe never blocks)",
			ctx:          FailModeContext{PolicyMode: edgecore.PolicyModeObserve, ActionIsKnownSafe: false, GatewayErrorCategory: GatewayErrorUnavailable},
			wantDecision: claude.DecisionAllow,
			wantDegraded: true,
			wantFailed:   false,
			wantTerminal: false,
		},
		{
			name:         "enforce + timeout + safe action -> allow",
			ctx:          FailModeContext{PolicyMode: edgecore.PolicyModeEnforce, ActionIsKnownSafe: true, GatewayErrorCategory: GatewayErrorTimeout},
			wantDecision: claude.DecisionAllow,
			wantDegraded: true,
			wantFailed:   false,
			wantTerminal: false,
		},
		{
			name:         "enforce + timeout + risky action -> deny fail-closed",
			ctx:          FailModeContext{PolicyMode: edgecore.PolicyModeEnforce, ActionIsKnownSafe: false, GatewayErrorCategory: GatewayErrorTimeout},
			wantDecision: claude.DecisionDeny,
			wantDegraded: true,
			wantFailed:   true,
			wantTerminal: true,
		},
		{
			name:         "enforce + malformed response + safe action -> allow",
			ctx:          FailModeContext{PolicyMode: edgecore.PolicyModeEnforce, ActionIsKnownSafe: true, GatewayErrorCategory: GatewayErrorMalformed},
			wantDecision: claude.DecisionAllow,
			wantDegraded: true,
		},
		{
			name:         "enterprise-strict + any miss + safe action -> deny fail-closed",
			ctx:          FailModeContext{PolicyMode: edgecore.PolicyModeEnterpriseStrict, ActionIsKnownSafe: true, GatewayErrorCategory: GatewayErrorUnavailable},
			wantDecision: claude.DecisionDeny,
			wantDegraded: true,
			wantFailed:   true,
			wantTerminal: true,
		},
		{
			name:         "enterprise-strict + policy_unavailable + safe action -> deny fail-closed",
			ctx:          FailModeContext{PolicyMode: edgecore.PolicyModeEnterpriseStrict, ActionIsKnownSafe: true, GatewayErrorCategory: GatewayErrorPolicyUnavailable},
			wantDecision: claude.DecisionDeny,
			wantDegraded: true,
			wantFailed:   true,
			wantTerminal: true,
		},
		{
			name:         "workflow requires-edge-governance + observe + safe -> deny (override observe)",
			ctx:          FailModeContext{PolicyMode: edgecore.PolicyModeObserve, WorkflowEdgeRequired: true, ActionIsKnownSafe: true, GatewayErrorCategory: GatewayErrorTimeout},
			wantDecision: claude.DecisionDeny,
			wantDegraded: true,
			wantFailed:   true,
			wantTerminal: true,
		},
		{
			name:         "empty policy mode defaults to observe -> allow",
			ctx:          FailModeContext{PolicyMode: "", ActionIsKnownSafe: true, GatewayErrorCategory: GatewayErrorUnavailable},
			wantDecision: claude.DecisionAllow,
			wantDegraded: true,
			wantFailed:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyFailMode(tc.ctx)
			if got.Decision != tc.wantDecision {
				t.Fatalf("decision = %q, want %q", got.Decision, tc.wantDecision)
			}
			if got.Degraded != tc.wantDegraded {
				t.Fatalf("degraded = %v, want %v", got.Degraded, tc.wantDegraded)
			}
			if got.FailClosed != tc.wantFailed {
				t.Fatalf("fail_closed = %v, want %v", got.FailClosed, tc.wantFailed)
			}
			if (got.TerminalCopy != "") != tc.wantTerminal {
				t.Fatalf("terminal copy presence = %v, want %v (got %q)", got.TerminalCopy != "", tc.wantTerminal, got.TerminalCopy)
			}
			if got.Reason == "" {
				t.Fatalf("reason should never be empty")
			}
			if tc.ctx.GatewayErrorCategory != "" && tc.ctx.GatewayErrorCategory != GatewayErrorNone {
				if !strings.Contains(got.Reason, string(tc.ctx.GatewayErrorCategory)) {
					t.Fatalf("reason %q should mention error category %q", got.Reason, tc.ctx.GatewayErrorCategory)
				}
			}
		})
	}
}

func TestApplyFailModeApprovalRetryAlwaysDenies(t *testing.T) {
	cases := []edgecore.PolicyMode{
		edgecore.PolicyModeObserve,
		edgecore.PolicyModeEnforce,
		edgecore.PolicyModeEnterpriseStrict,
	}
	for _, mode := range cases {
		t.Run(string(mode), func(t *testing.T) {
			out := ApplyFailMode(FailModeContext{
				PolicyMode:           mode,
				ActionIsKnownSafe:    true,
				ApprovalRef:          "edge_appr_test_123",
				GatewayErrorCategory: GatewayErrorTimeout,
			})
			if out.Decision != claude.DecisionDeny {
				t.Fatalf("approval retry in %s mode = %q, want deny", mode, out.Decision)
			}
			if !out.FailClosed {
				t.Fatalf("approval retry in %s mode must be fail-closed", mode)
			}
			if !strings.Contains(strings.ToLower(out.TerminalCopy), "not consumed") {
				t.Fatalf("approval retry terminal copy must say NOT consumed, got %q", out.TerminalCopy)
			}
			if !strings.Contains(out.Reason, "approval") {
				t.Fatalf("approval retry reason should mention approval, got %q", out.Reason)
			}
		})
	}
}

func TestApplyFailModeReasonAndTerminalCopyAreSanitized(t *testing.T) {
	// Inject a category and verify the strings stay short, never include a
	// payload-shaped pattern, and never echo a Bearer/sk- secret. ApplyFailMode
	// owns its strings and gets no caller-supplied content beyond the category
	// enum, so the assertion is structural: the output is bounded text.
	out := ApplyFailMode(FailModeContext{
		PolicyMode:           edgecore.PolicyModeEnterpriseStrict,
		GatewayErrorCategory: GatewayErrorPolicyUnavailable,
	})
	if len(out.Reason) > 256 {
		t.Fatalf("reason too long (%d bytes); fail-mode messages must stay terminal-friendly", len(out.Reason))
	}
	if len(out.TerminalCopy) > 256 {
		t.Fatalf("terminal copy too long (%d bytes)", len(out.TerminalCopy))
	}
	for _, pattern := range []string{"Bearer", "sk-", "api_key", "secret", "password"} {
		if strings.Contains(out.Reason, pattern) {
			t.Fatalf("reason must never echo secret-shaped pattern %q: %q", pattern, out.Reason)
		}
		if strings.Contains(out.TerminalCopy, pattern) {
			t.Fatalf("terminal copy must never echo secret-shaped pattern %q: %q", pattern, out.TerminalCopy)
		}
	}
}

func TestApplyFailModeHasFreshDecisionShouldNotBeInvoked(t *testing.T) {
	// Documents the calling-layer contract: ApplyFailMode is for the
	// degraded path. If a fresh decision exists the caller should use it
	// directly. This test asserts that even if HasFreshDecision is set true
	// by accident, the function still returns deterministic output (no
	// panics, no fall-through to nil claude.Decision).
	out := ApplyFailMode(FailModeContext{
		PolicyMode:       edgecore.PolicyModeEnforce,
		HasFreshDecision: true,
		// Note: GatewayErrorCategory empty too — looks like "everything is fine"
	})
	if out.Decision == "" {
		t.Fatalf("ApplyFailMode must always return a decision")
	}
}
