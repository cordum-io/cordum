package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

func Run(ctx context.Context, opts RunOptions) int {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	timeout := hookTimeout(opts)
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	input, err := readHookInput(readCtx, opts.Stdin, maxInputBytes(opts))
	cancel()
	if err != nil {
		writeInputError(stderr, err)
		return 2
	}
	if !supportedHookEvent(input.HookEventName) {
		warnf(stderr, "unsupported_hook_event event=%s session=%s", redactDiagnostic(input.HookEventName), safeID(input.SessionID))
		if failClosed(opts) {
			return 2
		}
		return 0
	}

	agentd := opts.Agentd
	if agentd == nil {
		client, err := NewHTTPAgentdClient(envValue(opts.Env, "CORDUM_AGENTD_URL"), timeout)
		if err != nil {
			return handleAgentdError(stderr, stdout, input, err, opts)
		}
		agentd = client
	}

	callCtx, callCancel := context.WithTimeout(ctx, timeout)
	decision, err := agentd.EvaluateHook(callCtx, agentdRequest(input, opts.Args, opts.Env))
	callCancel()
	if err != nil {
		return handleAgentdError(stderr, stdout, input, err, opts)
	}
	out := hookOutputForRun(input.HookEventName, decision, opts)
	if isEmptyOutput(out) {
		return 0
	}
	if err := writeJSON(stdout, out); err != nil {
		warnf(stderr, "hook_output_write_failed error=%s", redactDiagnostic(err.Error()))
		return 2
	}
	return 0
}

func writeInputError(w io.Writer, err error) {
	switch {
	case errors.Is(err, errInputTimeout):
		warnf(w, "hook_input_timeout")
	case errors.Is(err, errInputTooLarge):
		warnf(w, "hook_input_too_large")
	case errors.Is(err, errMalformedJSON), errors.Is(err, errMultipleJSON), errors.Is(err, errNonObjectJSON), errors.Is(err, errEmptyInput):
		warnf(w, "invalid_hook_json")
	default:
		warnf(w, "hook_input_error error=%s", redactDiagnostic(err.Error()))
	}
}

func handleAgentdError(stderr, stdout io.Writer, input HookInput, err error, opts RunOptions) int {
	code := "agentd_unavailable"
	reason := "Cordum Edge unavailable; blocking by fail-closed policy"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		code = "agentd_timeout"
		reason = "Cordum Edge timeout; blocking by fail-closed policy"
	}
	warnf(stderr, "%s error=%s", code, redactDiagnostic(err.Error()))
	if input.HookEventName == "FileChanged" {
		return 0
	}
	if failClosed(opts) {
		out := failClosedOutput(input.HookEventName, reason)
		if isEmptyOutput(out) {
			return 2
		}
		if werr := writeJSON(stdout, out); werr != nil {
			warnf(stderr, "hook_output_write_failed error=%s", redactDiagnostic(werr.Error()))
			return 2
		}
		return 0
	}
	if localDevEnforce(opts) && input.HookEventName == "PreToolUse" {
		localReason := "Cordum Edge local enforcer unavailable; blocking unclassified action"
		if riskyPreToolUse(input) {
			localReason = "Cordum Edge local enforcer unavailable; blocking risky action"
		}
		out := failClosedOutput(input.HookEventName, localReason)
		if werr := writeJSON(stdout, out); werr != nil {
			warnf(stderr, "hook_output_write_failed error=%s", redactDiagnostic(werr.Error()))
			return 2
		}
		return 0
	}
	return 0
}

func failClosedOutput(eventName, reason string) ClaudeHookOutput {
	switch eventName {
	case "PreToolUse":
		return ClaudeHookOutputForDecision(eventName, AgentdDecision{Decision: DecisionDeny, Reason: reason})
	case "UserPromptSubmit", "PostToolUse", "PostToolUseFailure":
		return ClaudeHookOutputForDecision(eventName, AgentdDecision{Decision: DecisionDeny, Reason: reason})
	case "ConfigChange":
		return ClaudeHookOutputForDecision(eventName, AgentdDecision{Decision: DecisionDeny, Reason: reason})
	default:
		return ClaudeHookOutput{}
	}
}

func failClosed(opts RunOptions) bool {
	return parseBool(envValue(opts.Env, "CORDUM_AGENTD_FAIL_CLOSED")) || strings.EqualFold(envValue(opts.Env, "CORDUM_EDGE_MODE"), "enterprise-strict")
}

func localDevEnforce(opts RunOptions) bool {
	mode := strings.ToLower(strings.TrimSpace(envValue(opts.Env, "CORDUM_EDGE_MODE")))
	return mode == "local-dev-enforce" || mode == "local-dev enforce"
}

func riskyPreToolUse(input HookInput) bool {
	if !strings.EqualFold(input.ToolName, "Bash") {
		return input.ToolName == ""
	}
	raw, ok := input.ToolInput["command"]
	if !ok {
		return true
	}
	command, ok := raw.(string)
	if !ok || strings.TrimSpace(command) == "" {
		return true
	}
	normalized := strings.ToLower(command)
	return strings.Contains(normalized, "rm -rf") ||
		strings.Contains(normalized, "rm -fr") ||
		strings.Contains(normalized, "sudo rm -rf") ||
		strings.Contains(normalized, "doas rm -rf")
}

func parseBool(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}

func supportedHookEvent(eventName string) bool {
	switch eventName {
	case "PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "ConfigChange", "FileChanged":
		return true
	default:
		return false
	}
}

func hookOutputForRun(eventName string, decision AgentdDecision, opts RunOptions) ClaudeHookOutput {
	switch eventName {
	case "ConfigChange":
		if !failClosed(opts) {
			return ClaudeHookOutput{}
		}
		return ClaudeHookOutputForDecision(eventName, decision)
	case "FileChanged":
		return ClaudeHookOutput{}
	default:
		return ClaudeHookOutputForDecision(eventName, decision)
	}
}

func agentdRequest(input HookInput, args []string, env map[string]string) AgentdRequest {
	return AgentdRequest{
		EventName:       input.HookEventName,
		SessionID:       input.SessionID,
		ExecutionID:     envValue(env, "CORDUM_EDGE_EXECUTION_ID"),
		TranscriptPath:  input.TranscriptPath,
		CWD:             input.CWD,
		PermissionMode:  input.PermissionMode,
		ToolName:        input.ToolName,
		ToolUseID:       input.ToolUseID,
		DurationMS:      input.DurationMS,
		Prompt:          input.Prompt,
		Source:          input.Source,
		FilePath:        input.FilePath,
		FileEvent:       input.FileEvent,
		ToolInput:       input.ToolInput,
		ToolResponse:    input.ToolResponse,
		RawPayload:      append([]byte(nil), input.RawPayload...),
		HookCommandArgs: append([]string(nil), args...),
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func warnf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format+"\n", args...)
}

func isEmptyOutput(out ClaudeHookOutput) bool {
	return out.Continue == nil && out.StopReason == "" && out.SuppressOutput == nil && out.SystemMessage == "" && out.Decision == "" && out.Reason == "" && out.HookSpecificOutput == nil
}

// Keep time imported for public timeout docs in generated package docs.
var _ = time.Second
