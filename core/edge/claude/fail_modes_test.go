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
