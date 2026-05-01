package claude

import (
	"context"
	"strings"
	"testing"
)

func TestRunConfigChangeForwardsToAgentdAndBlocksOnlyInEnterpriseStrict(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(_ context.Context, req AgentdRequest) (AgentdDecision, error) {
		if req.EventName != "ConfigChange" || req.Source != "project_settings" || req.FilePath != "/repo/.claude/settings.json" {
			t.Fatalf("unexpected ConfigChange request: %#v", req)
		}
		if len(req.RawPayload) == 0 || !strings.Contains(string(req.RawPayload), `"source":"project_settings"`) {
			t.Fatalf("raw payload not forwarded in-memory to agentd: %q", req.RawPayload)
		}
		return AgentdDecision{Decision: DecisionDeny, Reason: "configuration change blocked by Cordum policy"}, nil
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "config-change"},
		Stdin:  hookInput(`{"hook_event_name":"ConfigChange","source":"project_settings","file_path":"/repo/.claude/settings.json"}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enterprise-strict"},
	})

	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"decision":"block","reason":"configuration change blocked by Cordum policy","hookSpecificOutput":{"hookEventName":"ConfigChange"}}`)
	if stderr != "" {
		t.Fatalf("stderr should be empty for clean ConfigChange decision, got %q", stderr)
	}
	if agentd.calls != 1 {
		t.Fatalf("agentd calls=%d want 1", agentd.calls)
	}
}

func TestRunConfigChangeDoesNotBlockOutsideEnterpriseStrict(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(_ context.Context, req AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: DecisionDeny, Reason: "would block only in strict mode"}, nil
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "config-change"},
		Stdin:  hookInput(`{"hook_event_name":"ConfigChange","source":"local_settings","file_path":"/repo/.claude/settings.local.json"}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "observe"},
	})

	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout=%q, want empty non-blocking ConfigChange outside enterprise strict", stdout)
	}
}

func TestRunFileChangedForwardsToAgentdButNeverBlocks(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(_ context.Context, req AgentdRequest) (AgentdDecision, error) {
		if req.EventName != "FileChanged" || req.FilePath != "/repo/.envrc" || req.FileEvent != "change" {
			t.Fatalf("unexpected FileChanged request: %#v", req)
		}
		return AgentdDecision{Decision: DecisionDeny, Reason: "file change cannot be blocked"}, nil
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "file-changed"},
		Stdin:  hookInput(`{"hook_event_name":"FileChanged","file_path":"/repo/.envrc","event":"change"}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enterprise-strict"},
	})

	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout=%q, want empty because FileChanged has no decision control", stdout)
	}
	if strings.Contains(stderr, "/repo/.envrc") {
		t.Fatalf("stderr leaked raw file path: %q", stderr)
	}
	if agentd.calls != 1 {
		t.Fatalf("agentd calls=%d want 1", agentd.calls)
	}
}
