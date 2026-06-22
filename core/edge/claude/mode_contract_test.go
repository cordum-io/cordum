package claude

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestLocalDevEnforceRecognizesLauncherEnforceMode pins the mode-string
// contract between the launcher (settings.go emits CORDUM_EDGE_MODE = the
// policy-mode token) and the runner (localDevEnforce decides whether the
// degrade-closed path engages). It derives the env value from the real
// settings generator so it fails if EITHER side drifts the token.
func TestLocalDevEnforceRecognizesLauncherEnforceMode(t *testing.T) {
	data, err := GenerateDevSettingsJSON(DevSettingsOptions{
		PolicyMode:          "enforce",
		SessionID:           "s",
		ExecutionID:         "e",
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		ApprovalWaitTimeout: 30 * time.Second,
		Platform:            "linux",
	})
	if err != nil {
		t.Fatalf("GenerateDevSettingsJSON returned error: %v", err)
	}
	settings := decodeJSONMap(t, data)
	env := jsonObject(t, settings["env"])
	mode, ok := env["CORDUM_EDGE_MODE"].(string)
	if !ok || mode == "" {
		t.Fatalf("CORDUM_EDGE_MODE missing/non-string in generated settings env: %#v", env)
	}
	if !localDevEnforce(RunOptions{Env: map[string]string{"CORDUM_EDGE_MODE": mode}}) {
		t.Fatalf("localDevEnforce(CORDUM_EDGE_MODE=%q) = false, want true — launcher<->runner mode-string contract drift", mode)
	}
}

// TestRunEnforceDegradesClosedForRiskyPreToolUseWhenFailClosedUnset proves the
// defense-in-depth fallback: in enforce mode with agentd unavailable and
// CORDUM_AGENTD_FAIL_CLOSED UNSET (so failClosed() is false and the failClosed
// branch does not fire), a risky PreToolUse must still degrade closed (deny).
// Mirrors the local-dev-enforce case in fail_modes_test.go but with the
// canonical "enforce" token the launcher actually emits.
func TestRunEnforceDegradesClosedForRiskyPreToolUseWhenFailClosedUnset(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd stopped")
	}}
	code, stdout, stderr := runHook(t, RunOptions{
		Args:  []string{"claude", "pre-tool-use"},
		Stdin: hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/cordum-risk"}}`),
		// CORDUM_AGENTD_FAIL_CLOSED intentionally absent: this exercises the
		// localDevEnforce defense-in-depth path, NOT the failClosed branch.
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enforce"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge local enforcer unavailable; blocking risky action"}}`)
	if strings.Contains(stderr, "rm -rf") {
		t.Fatalf("stderr leaked raw command: %q", stderr)
	}
}
