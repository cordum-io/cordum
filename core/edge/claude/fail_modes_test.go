package claude

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunObserveModeAllowsNoopWhenAgentdUnavailable(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("connection refused sk-test-secret")
	}}
	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "observe"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("observe outage stdout=%q, want empty allow/no-op", stdout)
	}
	if !strings.Contains(stderr, "agentd_unavailable") {
		t.Fatalf("stderr missing degraded warning: %q", stderr)
	}
	assertNoSyntheticSecrets(t, stderr)
}

func TestRunLocalDevEnforceDeniesRiskyPreToolUseWhenAgentdUnavailable(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd stopped")
	}}
	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/cordum-risk"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "local-dev-enforce"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge local enforcer unavailable; blocking risky action"}}`)
	if strings.Contains(stderr, "rm -rf") {
		t.Fatalf("stderr leaked raw command: %q", stderr)
	}
}

func TestRunEnterpriseStrictDeniesMalformedAgentdResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":`))
	}))
	defer server.Close()
	code, stdout, stderr := runHook(t, RunOptions{
		Args:  []string{"claude", "pre-tool-use"},
		Stdin: hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Env: map[string]string{
			"CORDUM_AGENTD_URL": server.URL,
			"CORDUM_EDGE_MODE":  "enterprise-strict",
		},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge unavailable; blocking by fail-closed policy"}}`)
	if !strings.Contains(stderr, "agentd_unavailable") {
		t.Fatalf("stderr missing malformed-response warning: %q", stderr)
	}
}

// =====================================================================
// Regression: Edge end-to-end testing on 2026-05-28 uncovered that when
// agentd returned a *degraded* PreToolUse response (the gateway couldn't
// complete evaluation in the hook's 5s budget — agentd answered with
// `decision=RECORDED, degraded=true`), the hook dropped the degraded
// signal entirely (no field on AgentdDecision) and preToolUseOutput's
// `default:` arm returned an empty output → Claude proceeded with the
// risky action. Under `policy_mode=enforce` that's a silent fail-OPEN
// on every risky tool call whenever the safety kernel is briefly slow.
//
// Fix:
//   1. AgentdDecision now carries the Degraded field (matches the JSON
//      payload agentd has been emitting all along).
//   2. hookOutputForRun synthesizes a deny in enforce / enterprise-strict
//      modes when the response is flagged degraded, naming the mode in
//      the reason so the audit trail and the model both see why.
// =====================================================================

func TestRunEnforceDeniesDegradedPreToolUse(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		// What real agentd sends when the gateway evaluation didn't
		// complete in time. Decision is a placeholder; Degraded is true.
		return AgentdDecision{
			Decision: Decision("recorded"),
			Reason:   "evaluation pending",
			Degraded: true,
		}, nil
	}}
	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/x.txt","new_string":"hi"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_POLICY_MODE": "enforce"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	// Must be a deny output that names the degraded condition AND the
	// policy mode that caused fail-close. We assert substrings rather
	// than exact JSON because the synthesized reason concatenates strings.
	if stdout == "" {
		t.Fatalf("expected non-empty hook output, got empty (would silently allow)")
	}
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) {
		t.Errorf("output should deny, got: %s", stdout)
	}
	if !strings.Contains(stdout, "degraded") {
		t.Errorf("reason should mention degraded state; got: %s", stdout)
	}
	if !strings.Contains(stdout, "enforce") {
		t.Errorf("reason should name the policy_mode that forced fail-close; got: %s", stdout)
	}
}

func TestRunEnterpriseStrictDeniesDegradedPreToolUse(t *testing.T) {
	// Same fail-closed treatment when CORDUM_EDGE_MODE=enterprise-strict.
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: Decision("recorded"), Degraded: true}, nil
	}}
	code, stdout, _ := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/x.txt","new_string":"hi"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enterprise-strict"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) {
		t.Errorf("enterprise-strict must deny on degraded PreToolUse; got: %s", stdout)
	}
}

func TestRunObserveModeAllowsDegradedPreToolUse(t *testing.T) {
	// Observe mode is the explicit opposite: we record but never enforce.
	// A degraded response must still pass through to Claude as a no-op
	// — otherwise we'd be silently denying in a mode that's supposed to
	// be observability-only.
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: Decision("recorded"), Degraded: true}, nil
	}}
	code, stdout, _ := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/x.txt","new_string":"hi"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_POLICY_MODE": "observe"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	if stdout != "" {
		t.Errorf("observe mode should NOT synthesize a deny on degraded; got: %s", stdout)
	}
}

func TestRunEnforceAllowsNonDegradedRecordedResponses(t *testing.T) {
	// Belt-and-braces: a RECORDED decision WITHOUT the degraded flag means
	// the evaluation completed and the policy explicitly chose to record
	// (e.g. user prompt submit). That must still pass through cleanly —
	// the fail-close only triggers when degraded=true.
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: Decision("recorded"), Degraded: false}, nil
	}}
	code, stdout, _ := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/x.txt","new_string":"hi"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_POLICY_MODE": "enforce"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	if stdout != "" {
		t.Errorf("RECORDED without degraded should be a no-op pass-through; got: %s", stdout)
	}
}
