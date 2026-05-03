package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge/claude"
)

type cliFakeAgentd struct{}

func (cliFakeAgentd) EvaluateHook(context.Context, claude.AgentdRequest) (claude.AgentdDecision, error) {
	return claude.AgentdDecision{Decision: claude.DecisionDeny, Reason: "blocked by cli fake"}, nil
}

type cliFailingAgentd struct{}

func (cliFailingAgentd) EvaluateHook(context.Context, claude.AgentdRequest) (claude.AgentdDecision, error) {
	return claude.AgentdDecision{}, errors.New("agentd error sk-test-secret ghp_testtoken")
}

func TestRunCLIDelegatesClaudeHookAndKeepsStdoutJSONOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/x"}}`),
		Stdout: &stdout,
		Stderr: &stderr,
		Agentd: cliFakeAgentd{},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	var parsed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout must contain only valid JSON, got %q: %v", stdout.String(), err)
	}
	if !strings.Contains(stdout.String(), `"permissionDecision":"deny"`) {
		t.Fatalf("stdout missing deny decision: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "[CORDUM") || strings.Contains(stdout.String(), "INFO") {
		t.Fatalf("stdout contains log text: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "rm -rf") {
		t.Fatalf("stderr leaked raw command: %q", stderr.String())
	}
}

func TestRunCLIUsageAndErrorsStayOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"claude"},
		Stdin:  strings.NewReader("{}"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code == 0 {
		t.Fatalf("exit code should be non-zero for missing hook subcommand")
	}
	if stdout.String() != "" {
		t.Fatalf("stdout=%q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "usage:") || !strings.Contains(stderr.String(), "cordum-hook claude") {
		t.Fatalf("stderr missing usage, got %q", stderr.String())
	}
}

func TestRunCLIRedactsEnvAndAgentdErrorSecrets(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"echo sk-test-secret"}}`),
		Stdout: &stdout,
		Stderr: &stderr,
		Agentd: cliFailingAgentd{},
		Env: map[string]string{
			"CORDUM_AGENTD_FAIL_CLOSED": "true",
			"CORDUM_AGENTD_URL":         "http://127.0.0.1:7778/?token=ghp_testtoken",
		},
	})
	if code != 0 {
		t.Fatalf("strict parseable PreToolUse outage should emit deny JSON and exit 0, got code=%d stderr=%q", code, stderr.String())
	}
	combined := stdout.String() + stderr.String()
	for _, secret := range []string{"sk-test-secret", "ghp_testtoken"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("secret %q leaked in stdout=%q stderr=%q", secret, stdout.String(), stderr.String())
		}
	}
}

func TestRunCLISupportsConfigChangeSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"claude", "config-change"},
		Stdin:  strings.NewReader(`{"hook_event_name":"ConfigChange","source":"project_settings","file_path":"/repo/.claude/settings.json"}`),
		Stdout: &stdout,
		Stderr: &stderr,
		Agentd: cliFakeAgentd{},
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enterprise-strict"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"decision":"block"`) {
		t.Fatalf("stdout missing ConfigChange block decision: %q", stdout.String())
	}
}

func TestRunCLISupportsFileChangedSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"claude", "file-changed"},
		Stdin:  strings.NewReader(`{"hook_event_name":"FileChanged","file_path":"/repo/.envrc","event":"change"}`),
		Stdout: &stdout,
		Stderr: &stderr,
		Agentd: cliFakeAgentd{},
		Env:    map[string]string{"CORDUM_EDGE_MODE": "enterprise-strict"},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout=%q, want empty because FileChanged cannot block", stdout.String())
	}
}

func TestRunCLIRejectsUnsupportedClaudeSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"claude", "session-start"},
		Stdin:  strings.NewReader(`{"hook_event_name":"SessionStart"}`),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout=%q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr missing usage for unsupported subcommand: %q", stderr.String())
	}
}

func TestRunCLIRejectsExtraPositionalArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		// A correctly formed first two args, but a stray third arg that the
		// previous CLI loop would have silently forwarded into claude.Run.
		Args:   []string{"claude", "pre-tool-use", "unexpected"},
		Stdin:  strings.NewReader(`{"hook_event_name":"PreToolUse"}`),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 2 {
		t.Fatalf("exit code=%d want 2", code)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout=%q, want empty (stdout is reserved for hook JSON)", stdout.String())
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr missing usage for extra positional args: %q", stderr.String())
	}
}
