package main

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge/claude"
)

// cliCapturingAgentd records every AgentdRequest cordum-hook sends so tests
// can assert on the classifier-derived Capability/RiskTags. Unlike
// cliFakeAgentd (which returns DecisionDeny unconditionally, regardless of
// input), this fake exercises and observes the REAL ClassifyEvent output —
// AgentdRequest.Capability/RiskTags are populated by claude.MapHookInput,
// which calls edge.ClassifyEvent, not by anything the fake makes up.
type cliCapturingAgentd struct {
	requests []claude.AgentdRequest
}

func (c *cliCapturingAgentd) EvaluateHook(_ context.Context, req claude.AgentdRequest) (claude.AgentdDecision, error) {
	c.requests = append(c.requests, req)
	return claude.AgentdDecision{Decision: claude.DecisionAllow}, nil
}

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

// TestCopilotRunInTerminalClassifiesLikeClaudeBash proves cordum-hook's
// Copilot adapter routes Copilot's native tool_name (run_in_terminal)
// through the SAME server-side classification (edge.ClassifyEvent, invoked
// via claude.MapHookInput/agentdRequest) that Claude Code's "Bash" tool_name
// gets, for the identical destructive command. Pre-fix, run_in_terminal was
// not among classifyHookEvent's recognized tool names, so it fell into the
// default branch (capability=edge.unknown) and none of the capability-keyed
// policy rules (e.g. claude-code.deny-destructive-shell, which matches
// capability=exec.shell) ever fired for Copilot — regardless of how
// dangerous the actual command was. This test bypasses cliFakeAgentd (which
// denies unconditionally and would pass whether or not classification ever
// ran) and instead captures the real AgentdRequest cordum-hook builds.
func TestCopilotRunInTerminalClassifiesLikeClaudeBash(t *testing.T) {
	const command = `rm -rf /important/data`

	copilotAgentd := &cliCapturingAgentd{}
	copilotCode := runCLI(context.Background(), cliOptions{
		Args:   []string{"copilot", "pre-tool-use"},
		Stdin:  strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"run_in_terminal","tool_input":{"command":"` + command + `"}}`),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Agentd: copilotAgentd,
	})
	if copilotCode != 0 {
		t.Fatalf("copilot run_in_terminal: exit code=%d", copilotCode)
	}
	if len(copilotAgentd.requests) != 1 {
		t.Fatalf("copilot run_in_terminal: expected exactly one agentd call, got %d", len(copilotAgentd.requests))
	}

	claudeAgentd := &cliCapturingAgentd{}
	claudeCode := runCLI(context.Background(), cliOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"` + command + `"}}`),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Agentd: claudeAgentd,
	})
	if claudeCode != 0 {
		t.Fatalf("claude bash: exit code=%d", claudeCode)
	}
	if len(claudeAgentd.requests) != 1 {
		t.Fatalf("claude bash: expected exactly one agentd call, got %d", len(claudeAgentd.requests))
	}

	copilotReq := copilotAgentd.requests[0]
	claudeReq := claudeAgentd.requests[0]

	if copilotReq.Capability != "exec.shell" {
		t.Fatalf("copilot run_in_terminal capability = %q, want %q (Copilot's tool_name was not normalized into a bucket classifyHookEvent recognizes)", copilotReq.Capability, "exec.shell")
	}
	if copilotReq.Capability != claudeReq.Capability {
		t.Fatalf("copilot capability %q != claude capability %q for the same destructive command", copilotReq.Capability, claudeReq.Capability)
	}
	if !slices.Contains(copilotReq.RiskTags, "destructive") {
		t.Fatalf("copilot run_in_terminal risk_tags = %v, want to contain %q", copilotReq.RiskTags, "destructive")
	}
	if !slices.Equal(copilotReq.RiskTags, claudeReq.RiskTags) {
		t.Fatalf("copilot risk_tags %v != claude risk_tags %v for the same destructive command", copilotReq.RiskTags, claudeReq.RiskTags)
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
