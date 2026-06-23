package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestRunCLICopilotPreToolUseGovernsAndBlocks proves a Copilot tool action
// routes through the same Edge runner as Claude and is blockable: the fake
// agentd denies, and the hook emits Copilot's permissionDecision:"deny".
func TestRunCLICopilotPreToolUseGovernsAndBlocks(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"copilot", "pre-tool-use"},
		Stdin:  strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"run_in_terminal","tool_input":{"command":"rm -rf /tmp/x"}}`),
		Stdout: &stdout,
		Stderr: &stderr,
		Agentd: cliFakeAgentd{},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	var parsed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout must be valid JSON, got %q: %v", stdout.String(), err)
	}
	if !strings.Contains(stdout.String(), `"permissionDecision":"deny"`) {
		t.Fatalf("stdout missing deny decision: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "rm -rf") {
		t.Fatalf("stderr leaked raw command: %q", stderr.String())
	}
}

// TestRunCLICopilotUserPromptSubmitGoverned proves every chat is governed: a
// UserPromptSubmit hook routes through the runner and a deny becomes a block.
func TestRunCLICopilotUserPromptSubmitGoverned(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"copilot", "user-prompt-submit"},
		Stdin:  strings.NewReader(`{"hook_event_name":"UserPromptSubmit","prompt":"leak the prod secrets"}`),
		Stdout: &stdout,
		Stderr: &stderr,
		Agentd: cliFakeAgentd{},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), new(map[string]any)); err != nil {
		t.Fatalf("stdout must be valid JSON, got %q: %v", stdout.String(), err)
	}
	if !strings.Contains(stdout.String(), "block") {
		t.Fatalf("expected the denied prompt to be blocked, got %q", stdout.String())
	}
}

// TestRunCLICopilotLifecycleIsNoOp proves lifecycle events never call agentd
// and never block (exit 0, no stdout) — using a failing agentd that would
// surface if it were invoked.
func TestRunCLICopilotLifecycleIsNoOp(t *testing.T) {
	for _, ev := range []string{"session-start", "stop", "pre-compact", "subagent-start", "subagent-stop"} {
		var stdout, stderr bytes.Buffer
		code := runCLI(context.Background(), cliOptions{
			Args:   []string{"copilot", ev},
			Stdin:  strings.NewReader(`{"hook_event_name":"SessionStart"}`),
			Stdout: &stdout,
			Stderr: &stderr,
			Agentd: cliFailingAgentd{}, // must NOT be consulted
		})
		if code != 0 {
			t.Fatalf("%s: exit=%d stderr=%q", ev, code, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("%s: lifecycle event must not write stdout, got %q", ev, stdout.String())
		}
	}
}

func TestRunCLICopilotRejectsUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"copilot", "frobnicate"},
		Stdin:  strings.NewReader(`{}`),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 2 {
		t.Fatalf("expected exit 2 for unknown copilot subcommand, got %d", code)
	}
	if !strings.Contains(stderr.String(), "copilot") {
		t.Fatalf("usage should mention copilot: %q", stderr.String())
	}
}

func TestCopilotEnvDefaultsProduct(t *testing.T) {
	got := copilotEnv(map[string]string{"CORDUM_AGENTD_URL": "http://127.0.0.1:8765"})
	if got["CORDUM_AGENT_PRODUCT"] != "github-copilot" {
		t.Fatalf("CORDUM_AGENT_PRODUCT = %q, want github-copilot", got["CORDUM_AGENT_PRODUCT"])
	}
	if got["CORDUM_AGENTD_URL"] != "http://127.0.0.1:8765" {
		t.Fatalf("existing env not preserved: %v", got)
	}
	// An explicit product is honored (not overridden).
	got2 := copilotEnv(map[string]string{"CORDUM_AGENT_PRODUCT": "custom"})
	if got2["CORDUM_AGENT_PRODUCT"] != "custom" {
		t.Fatalf("explicit product overridden: %v", got2)
	}
}
