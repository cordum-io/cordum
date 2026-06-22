package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// fail_mode_matrix_test.go is the epic-8c29308d lock-in: a regression matrix
// proving a cordumctl-edge session in an enforcing mode FAILS CLOSED when the
// local agentd errors or times out, across tools. It exercises the real Run()
// path with a stub AgentdClient. The three matrices each lock a sibling fix and
// would FAIL on the pre-fix code (see the per-matrix "FAILS PRE-FIX" notes):
//   - Matrix 1 (TestFailModeMatrixFailClosed)      -> task-2781fa7e (FailClosed wiring)
//   - Matrix 2 (TestFailModeMatrixDegradeClosed)   -> task-cb0857fc + task-191754cc
//   - Matrix 3 (TestFailModeMatrixRequireApproval) -> require_approval -> deny guard
//
// An end-to-end variant that boots a real agentd, kills it, and verifies a Write
// is blocked is intentionally DEFERRED as a follow-up (heavyweight/flaky with
// CGO/-race off on this platform); it is not part of the DoD.

// sessionEnvForMode derives the Claude-side session env the launcher actually
// writes for a policy mode, via the real writeLaunchSettings path. task-2781fa7e
// computes CORDUM_AGENTD_FAIL_CLOSED here, so returning the GENERATED env (not a
// hand-set flag) is what makes Matrix 1 genuinely lock the settings-generation
// fix rather than the never-broken handleAgentdError branch.
func sessionEnvForMode(t *testing.T, policyMode string) map[string]string {
	t.Helper()
	cfg := launchConfig{
		PolicyMode:          policyMode,
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		ApprovalWaitTimeout: 30 * time.Second,
		TenantID:            "tenant-test",
		HookCommand:         "cordum-hook",
	}
	_, settings, err := writeLaunchSettings(t.TempDir(), cfg, LaunchMetadata{PrincipalID: "user-1"}, launchSessionState{SessionID: "s", ExecutionID: "e"})
	if err != nil {
		t.Fatalf("writeLaunchSettings(policyMode=%q): %v", policyMode, err)
	}
	envObj := jsonObject(t, decodeJSONMap(t, settings)["env"])
	env := make(map[string]string, len(envObj))
	for k, v := range envObj {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("generated env[%s] = %T, want string", k, v)
		}
		env[k] = s
	}
	return env
}

// preToolUseStdin builds PreToolUse hook stdin for a tool with a realistic
// tool_input shape (Bash carries the supplied command via toolInput).
func preToolUseStdin(t *testing.T, toolName string, toolInput map[string]any) io.Reader {
	t.Helper()
	payload := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       toolName,
		"tool_input":      toolInput,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal PreToolUse stdin for %q: %v", toolName, err)
	}
	return strings.NewReader(string(data))
}

// matrixTools are the file/shell tools the matrices drive. Bash's command is
// overridden per matrix (echo hi / rm -rf / npm test).
func matrixTools(bashCommand string) []struct {
	name  string
	input map[string]any
} {
	return []struct {
		name  string
		input map[string]any
	}{
		{"Write", map[string]any{"file_path": "/tmp/cordum-test.txt", "content": "data"}},
		{"Edit", map[string]any{"file_path": "/tmp/cordum-test.txt", "old_string": "a", "new_string": "b"}},
		{"Bash", map[string]any{"command": bashCommand}},
	}
}

func denyJSON(reason string) string {
	return `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + reason + `"}}`
}

// TestFailModeMatrixFailClosed locks task-2781fa7e. Using the env the launcher
// actually generates, enforce + enterprise-strict carry
// CORDUM_AGENTD_FAIL_CLOSED=true, so an agentd error/timeout denies EVERY tool
// via the failClosed branch; observe stays fail-open (empty stdout).
//
// FAILS PRE-FIX: before task-2781fa7e, writeLaunchSettings omitted FailClosed,
// so enforce/enterprise-strict env had CORDUM_AGENTD_FAIL_CLOSED=false ->
// handleAgentdError took the degraded-allow path -> empty stdout, NOT deny.
func TestFailModeMatrixFailClosed(t *testing.T) {
	failures := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"conn_refused", errors.New("connection refused"), "Cordum Edge unavailable; blocking by fail-closed policy"},
		{"deadline_exceeded", context.DeadlineExceeded, "Cordum Edge timeout; blocking by fail-closed policy"},
	}
	modes := []struct {
		mode     string
		wantDeny bool
	}{
		{"enforce", true},
		{"enterprise-strict", true},
		{"observe", false},
	}

	for _, m := range modes {
		env := sessionEnvForMode(t, m.mode)
		for _, tool := range matrixTools("echo hi") {
			for _, f := range failures {
				name := m.mode + "/" + tool.name + "/" + f.name
				t.Run(name, func(t *testing.T) {
					failErr := f.err
					agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
						return AgentdDecision{}, failErr
					}}
					code, stdout, stderr := runHook(t, RunOptions{
						Args:   []string{"claude", "pre-tool-use"},
						Stdin:  preToolUseStdin(t, tool.name, tool.input),
						Agentd: agentd,
						Env:    env,
					})
					if code != 0 {
						t.Fatalf("%s: exit code=%d, want 0 (stderr=%q)", name, code, stderr)
					}
					if m.wantDeny {
						assertCompactJSON(t, stdout, denyJSON(f.wantReason))
					} else if stdout != "" {
						t.Fatalf("%s: observe must fail OPEN, got stdout=%q", name, stdout)
					}
				})
			}
		}
	}
}

// TestFailModeMatrixDegradeClosed locks task-cb0857fc + task-191754cc (the
// defense-in-depth path). With CORDUM_EDGE_MODE=enforce and
// CORDUM_AGENTD_FAIL_CLOSED UNSET (so failClosed() is false), an agentd error
// still denies a risky PreToolUse via the localDevEnforce branch with the
// local-enforcer reason. This requires both "enforce" to engage localDevEnforce
// (cb0857fc) and Write/Edit to be classified risky (191754cc).
//
// FAILS PRE-FIX: pre-cb0857fc localDevEnforce did not match "enforce" -> degraded
// to allow (empty stdout); and pre-191754cc Write/Edit were "unclassified" -> the
// reason would be "...blocking unclassified action", not "...blocking risky action".
func TestFailModeMatrixDegradeClosed(t *testing.T) {
	const wantReason = "Cordum Edge local enforcer unavailable; blocking risky action"
	for _, tool := range matrixTools("rm -rf /tmp/x") {
		t.Run(tool.name, func(t *testing.T) {
			agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
				return AgentdDecision{}, errors.New("connection refused")
			}}
			code, stdout, stderr := runHook(t, RunOptions{
				Args:  []string{"claude", "pre-tool-use"},
				Stdin: preToolUseStdin(t, tool.name, tool.input),
				// CORDUM_AGENTD_FAIL_CLOSED intentionally ABSENT: exercises the
				// localDevEnforce defense-in-depth path, not the failClosed branch.
				Agentd: agentd,
				Env:    map[string]string{"CORDUM_EDGE_MODE": "enforce"},
			})
			if code != 0 {
				t.Fatalf("%s: exit code=%d, want 0 (stderr=%q)", tool.name, code, stderr)
			}
			assertCompactJSON(t, stdout, denyJSON(wantReason))
		})
	}
}

// TestFailModeMatrixRequireApproval guards the require_approval -> deny mapping:
// when agentd ANSWERS with require_approval (no error), the hook must still emit
// permissionDecision:"deny" + exit 0 so Claude does not silently proceed. This
// is the success path (no agentd error), distinct from the fail-closed branches.
func TestFailModeMatrixRequireApproval(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: DecisionRequireApproval, Reason: "approval required", ApprovalRef: "edge_appr_1"}, nil
	}}
	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  preToolUseStdin(t, "Bash", map[string]any{"command": "npm test"}),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enforce"},
	})
	if code != 0 {
		t.Fatalf("require_approval: exit code=%d, want 0 (stderr=%q)", code, stderr)
	}
	assertCompactJSON(t, stdout, denyJSON("approval required; approval_ref=edge_appr_1; approve then retry the tool call"))
}
