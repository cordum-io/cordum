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
	assertCompactJSON(t, stdout, `{"decision":"block","reason":"Cordum Edge local enforcer unavailable; blocking risky action","hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge local enforcer unavailable; blocking risky action"}}`)
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
	assertCompactJSON(t, stdout, `{"decision":"block","reason":"Cordum Edge unavailable; blocking by fail-closed policy","hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge unavailable; blocking by fail-closed policy"}}`)
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

// =====================================================================
// Regression for the deeper enforce-mode gap end-to-end testing on
// 2026-05-29 uncovered: when agentd was unreachable (Post EOF — agentd
// took longer than the hook's postBudget waiting for an approval that
// never came), the hook fell into handleAgentdError. failClosed(opts)
// returned false because it only matched enterprise-strict, not enforce.
// localDevEnforce(opts) returned false because mode was "enforce", not
// "local-dev-enforce". So the hook hit the catch-all "return 0" with 0
// bytes of stdout — a silent allow on every risky tool call in enforce
// mode whenever agentd was briefly slow.
//
// The fix wires enforce into the same fail-closed-on-risky branch
// local-dev-enforce already used, AND extends riskyPreToolUse to cover
// the file-mutation tools (Write / Edit / MultiEdit / NotebookEdit) that
// the demo policy pack's require-approval-for-edits rule was already
// trying to gate.
// =====================================================================

func TestRunEnforceModeDeniesWritePreToolUseWhenAgentdUnavailable(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd connection refused")
	}}
	code, stdout, _ := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/tmp/x","content":"hi"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enforce"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	if stdout == "" {
		t.Fatal("enforce mode + Write + agentd unavailable must NOT silently allow")
	}
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny output; got: %s", stdout)
	}
	if !strings.Contains(stdout, "blocking risky action") {
		t.Errorf("Write is a file-mutation tool and must be classified risky; got: %s", stdout)
	}
}

func TestRunEnforceModeDeniesEditPreToolUseWhenAgentdUnavailable(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd stopped")
	}}
	_, stdout, _ := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/x","old_string":"a","new_string":"b"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_POLICY_MODE": "enforce"},
	})
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) || !strings.Contains(stdout, "risky action") {
		t.Errorf("Edit under enforce + agentd-down must deny + classify risky; got: %s", stdout)
	}
}

func TestRunEnforceModeDeniesMultiEditPreToolUseWhenAgentdUnavailable(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd stopped")
	}}
	_, stdout, _ := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"MultiEdit","tool_input":{"file_path":"/tmp/x"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enforce"},
	})
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) {
		t.Errorf("MultiEdit must be denied under enforce + agentd-down; got: %s", stdout)
	}
}

func TestRunEnforceModeDeniesNotebookEditPreToolUseWhenAgentdUnavailable(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd stopped")
	}}
	_, stdout, _ := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"NotebookEdit","tool_input":{"notebook_path":"/tmp/n.ipynb"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enforce"},
	})
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) {
		t.Errorf("NotebookEdit must be denied under enforce + agentd-down; got: %s", stdout)
	}
}

func TestRunObserveModeAllowsWritePreToolUseWhenAgentdUnavailable(t *testing.T) {
	// Belt-and-braces: observe mode must NOT be wrapped into the new
	// enforce branch — operators who chose observability-only deployments
	// rely on it never synthesizing a deny.
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd connection refused")
	}}
	code, stdout, _ := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/tmp/x","content":"hi"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "observe"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	if stdout != "" {
		t.Errorf("observe mode must continue passing through degraded; got: %s", stdout)
	}
}

func TestRunEnforceModeDeniesUnclassifiedPreToolUseWhenAgentdUnavailable(t *testing.T) {
	// Tool name unknown to riskyPreToolUse (not Bash, not in
	// fileMutationTools) — must still deny under enforce because we
	// can't classify, and enforce "degrades closed for unknown actions".
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd unavailable")
	}}
	_, stdout, _ := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"FutureExperimentalTool","tool_input":{"x":"y"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enforce"},
	})
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) {
		t.Errorf("unknown tool under enforce + agentd-down must deny; got: %s", stdout)
	}
}

// CodeRabbit on #319 caught that the observability recording fired
// BEFORE the degraded+enforce synthesis path, so a fail-closed PreToolUse
// showed up in metrics as the original (non-deny) decision and with
// degraded/failClosed flags both false. This regression asserts that the
// recorder now sees DecisionDeny + degraded=true + failClosed=true with
// the canonical "degraded_policy_enforced" reason code, matching the
// hook's actual emitted output.
func TestRunEnforceDegradedRecordsDenyObservability(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{
			Decision: Decision("recorded"),
			Reason:   "evaluation pending",
			Degraded: true,
		}, nil
	}}
	recorder := &hookRecordingRecorder{}
	code, stdout, _ := runHook(t, RunOptions{
		Args:     []string{"claude", "pre-tool-use"},
		Stdin:    hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/tmp/x","content":"hi"}}`),
		Agentd:   agentd,
		Recorder: recorder,
		Env:      map[string]string{"CORDUM_EDGE_POLICY_MODE": "enforce"},
	})
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) {
		t.Fatalf("expected deny output; got: %s", stdout)
	}
	// 1. RecordActionDecision must reflect the SYNTHESIZED deny, not the
	//    original RECORDED decision.
	if len(recorder.actionDecisions) != 1 {
		t.Fatalf("want 1 RecordActionDecision call, got %d: %#v", len(recorder.actionDecisions), recorder.actionDecisions)
	}
	if got := recorder.actionDecisions[0].decision; got != string(DecisionDeny) {
		t.Errorf("RecordActionDecision.decision = %q, want %q (synthesized deny)", got, DecisionDeny)
	}
	if got := recorder.actionDecisions[0].mode; got != "enforce" {
		t.Errorf("RecordActionDecision.mode = %q, want %q", got, "enforce")
	}
	// 2. RecordActionDenied must fire — old code never reached this
	//    branch because effectiveDecision was the original RECORDED.
	if len(recorder.actionDenied) != 1 {
		t.Fatalf("want 1 RecordActionDenied call, got %d: %#v", len(recorder.actionDenied), recorder.actionDenied)
	}
	if got := recorder.actionDenied[0].reason; got != "degraded_policy_enforced" {
		t.Errorf("RecordActionDenied.reason = %q, want %q", got, "degraded_policy_enforced")
	}
	// 3. RecordDegraded must fire with the canonical reason code so
	//    operators can query degraded_policy_enforced specifically.
	if len(recorder.degraded) != 1 {
		t.Fatalf("want 1 RecordDegraded call, got %d", len(recorder.degraded))
	}
	if got := recorder.degraded[0].reason; got != "degraded_policy_enforced" {
		t.Errorf("RecordDegraded.reason = %q, want %q", got, "degraded_policy_enforced")
	}
	// 4. RecordFailClosed must fire — the hook truly fail-closed.
	if len(recorder.failClosed) != 1 {
		t.Fatalf("want 1 RecordFailClosed call, got %d", len(recorder.failClosed))
	}
}

// Symmetric guard: observe mode keeps reporting the raw decision and does
// NOT flip degraded / failClosed flags. Without this, future refactors of
// the synthesis branch could silently start emitting fail-closed metrics
// for observe deployments, hurting the audit story for opt-in tiers.
func TestRunObserveDegradedKeepsRawObservability(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: Decision("recorded"), Degraded: true}, nil
	}}
	recorder := &hookRecordingRecorder{}
	_, _, _ = runHook(t, RunOptions{
		Args:     []string{"claude", "pre-tool-use"},
		Stdin:    hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/tmp/x","content":"hi"}}`),
		Agentd:   agentd,
		Recorder: recorder,
		Env:      map[string]string{"CORDUM_EDGE_MODE": "observe"},
	})
	if len(recorder.actionDecisions) != 1 || recorder.actionDecisions[0].decision != "recorded" {
		t.Errorf("observe mode must keep the raw decision; got: %#v", recorder.actionDecisions)
	}
	if len(recorder.failClosed) != 0 {
		t.Errorf("observe mode must not record fail-closed; got %d calls", len(recorder.failClosed))
	}
}
