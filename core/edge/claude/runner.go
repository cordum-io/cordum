package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func Run(ctx context.Context, opts RunOptions) int {
	startedAt := time.Now()
	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	// EDGE-071: route the package-level redaction recorder through the
	// caller-supplied Recorder so metric emissions in mapper.go's
	// fail-closed branches reach the same registry as the rest of the
	// hook's edge metrics. Idempotent and concurrency-safe via
	// SetRedactionRecorder's mutex.
	if opts.Recorder != nil {
		SetRedactionRecorder(opts.Recorder)
	}

	timeout, err := hookTimeout(opts)
	if err != nil {
		warnf(stderr, "invalid_hook_timeout error=%s", redactDiagnostic(err.Error()))
		return 2
	}
	// One outer budget keeps the whole hook under Claude's 5s command-hook
	// deadline. The agentd POST gets a shorter child budget below so a slow
	// local daemon cannot consume the response-write reserve.
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	postBudget := agentdPostBudget(opts, timeout)

	input, err := readHookInput(runCtx, opts.Stdin, maxInputBytes(opts))
	if err != nil {
		if errors.Is(err, errInputTimeout) {
			recordHookTimeout(opts, "request")
		}
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
	req := agentdRequest(input, opts.Args, opts.Env)

	agentd, err := hookAgentdClient(opts, postBudget)
	if err != nil {
		return handleAgentdError(stderr, stdout, input, req, err, opts, startedAt)
	}
	decision, err := evaluateAgentdHook(runCtx, agentd, req, postBudget)
	if err != nil {
		return handleAgentdError(stderr, stdout, input, req, err, opts, startedAt)
	}
	recordHookObservability(opts, req, decision.Decision, hookDenyReasonCode(req, ""), false, false, startedAt)
	return writeRunOutput(stderr, stdout, input.HookEventName, decision, opts)
}

func hookAgentdClient(opts RunOptions, postBudget time.Duration) (AgentdClient, error) {
	if opts.Agentd != nil {
		return opts.Agentd, nil
	}
	return NewHTTPAgentdClientWithNonce(
		envValue(opts.Env, "CORDUM_AGENTD_URL"),
		postBudget,
		envValue(opts.Env, "CORDUM_AGENTD_HOOK_NONCE"),
	)
}

func evaluateAgentdHook(ctx context.Context, agentd AgentdClient, req AgentdRequest, postBudget time.Duration) (AgentdDecision, error) {
	agentdCtx, agentdCancel := context.WithTimeout(ctx, postBudget)
	defer agentdCancel()
	return agentd.EvaluateHook(agentdCtx, req)
}

func writeRunOutput(stderr, stdout io.Writer, eventName string, decision AgentdDecision, opts RunOptions) int {
	out := hookOutputForRun(eventName, decision, opts)
	if isEmptyOutput(out) {
		return 0
	}
	if err := writeJSON(stdout, out); err != nil {
		warnf(stderr, "hook_output_write_failed error=%s", redactDiagnostic(err.Error()))
		return 2
	}
	return 0
}

func agentdPostBudget(opts RunOptions, hookBudget time.Duration) time.Duration {
	maxPostBudget := hookBudget - ResponseWriteReserve
	if maxPostBudget <= 0 {
		maxPostBudget = hookBudget
	}
	budget := DefaultAgentdPostBudget
	if opts.AgentdPostBudget > 0 {
		budget = opts.AgentdPostBudget
	}
	if budget > maxPostBudget {
		return maxPostBudget
	}
	return budget
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

func handleAgentdError(stderr, stdout io.Writer, input HookInput, req AgentdRequest, err error, opts RunOptions, startedAt time.Time) int {
	code := "agentd_unavailable"
	reason := "Cordum Edge unavailable; blocking by fail-closed policy"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		code = "agentd_timeout"
		reason = "Cordum Edge timeout; blocking by fail-closed policy"
		recordHookTimeout(opts, "gateway")
	}
	warnf(stderr, "%s error=%s", code, redactDiagnostic(err.Error()))
	if input.HookEventName == "FileChanged" {
		recordHookObservability(opts, req, DecisionAllow, code, true, false, startedAt)
		return 0
	}
	if failClosed(opts) {
		out := failClosedOutput(input.HookEventName, reason)
		if isEmptyOutput(out) {
			recordHookObservability(opts, req, DecisionDeny, code, true, true, startedAt)
			return 2
		}
		if werr := writeJSON(stdout, out); werr != nil {
			warnf(stderr, "hook_output_write_failed error=%s", redactDiagnostic(werr.Error()))
			recordHookObservability(opts, req, DecisionDeny, "hook_output_write_failed", true, true, startedAt)
			return 2
		}
		recordHookObservability(opts, req, DecisionDeny, code, true, true, startedAt)
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
			recordHookObservability(opts, req, DecisionDeny, "hook_output_write_failed", true, false, startedAt)
			return 2
		}
		recordHookObservability(opts, req, DecisionDeny, code, true, false, startedAt)
		return 0
	}
	recordHookObservability(opts, req, Decision("degraded"), code, true, false, startedAt)
	return 0
}

func recordHookTimeout(opts RunOptions, phase string) {
	if opts.Recorder != nil {
		opts.Recorder.RecordHookTimeout(phase)
	}
}

func recordHookObservability(opts RunOptions, req AgentdRequest, decision Decision, reasonCode string, degraded, failClosed bool, startedAt time.Time) {
	rec := opts.Recorder
	if rec == nil {
		return
	}
	tenant := req.TenantID
	layer := strings.TrimSpace(req.Layer)
	if layer == "" {
		layer = string(edgecore.LayerHook)
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		if mappedKind, ok := mapHookEventToKind(req.EventName); ok {
			kind = string(mappedKind)
		}
	}
	decisionLabel := strings.TrimSpace(string(decision))
	if decisionLabel == "" {
		decisionLabel = "unknown"
	}
	mode := hookPolicyMode(opts)
	rec.RecordActionDecision(tenant, layer, kind, decisionLabel, mode)
	if decision == DecisionDeny {
		rec.RecordActionDenied(tenant, layer, kind, hookDenyReasonCode(req, reasonCode))
	}
	if degraded {
		rec.RecordDegraded(tenant, mode, "hook", hookStableReasonCode(reasonCode))
	}
	if failClosed {
		rec.RecordFailClosed(tenant, mode, hookStableReasonCode(reasonCode))
	}
	rec.ObserveHookLatency(tenant, req.EventName, decisionLabel, observedHookDuration(startedAt))
}

func hookPolicyMode(opts RunOptions) string {
	if mode := strings.TrimSpace(envValue(opts.Env, managedPolicyModeEnv)); mode != "" {
		return mode
	}
	if mode := strings.TrimSpace(envValue(opts.Env, "CORDUM_EDGE_MODE")); mode != "" {
		return mode
	}
	if parseBool(envValue(opts.Env, "CORDUM_AGENTD_FAIL_CLOSED")) {
		return string(edgecore.PolicyModeEnterpriseStrict)
	}
	return string(edgecore.PolicyModeObserve)
}

func hookDenyReasonCode(req AgentdRequest, fallback string) string {
	if reason := strings.TrimSpace(fallback); reason != "" {
		return reason
	}
	if reason := strings.TrimSpace(req.ReasonCode); reason != "" {
		return reason
	}
	return "policy_denied"
}

func hookStableReasonCode(reason string) string {
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		return trimmed
	}
	return "unknown"
}

func observedHookDuration(startedAt time.Time) time.Duration {
	if startedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(startedAt)
	if elapsed < 0 {
		return 0
	}
	return elapsed
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
		// ConfigChange is enforced only in enterprise-strict (fail-closed)
		// mode by design — see TestRunConfigChangeDoesNotBlockOutsideEnterprise
		// Strict. Outside strict mode the user is on a personal/dev machine
		// and we still record the event but do not surface a deny back to
		// Claude.
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
	req := AgentdRequest{
		EventName:       input.HookEventName,
		SessionID:       redactHookBoundaryString(input.SessionID),
		ExecutionID:     redactHookBoundaryString(envValue(env, "CORDUM_EDGE_EXECUTION_ID")),
		TranscriptPath:  redactHookBoundaryString(input.TranscriptPath),
		CWD:             redactHookBoundaryString(input.CWD),
		PermissionMode:  redactHookBoundaryString(input.PermissionMode),
		ToolName:        redactHookBoundaryString(input.ToolName),
		ToolUseID:       redactHookBoundaryString(input.ToolUseID),
		DurationMS:      input.DurationMS,
		Prompt:          redactHookBoundaryString(input.Prompt),
		Source:          redactHookBoundaryString(input.Source),
		FilePath:        redactHookBoundaryString(input.FilePath),
		FileEvent:       redactHookBoundaryString(input.FileEvent),
		ToolInput:       redactHookBoundaryMap(input.ToolInput),
		ToolResponse:    redactHookBoundaryMap(input.ToolResponse),
		RawPayload:      append([]byte(nil), input.RawPayload...),
		HookCommandArgs: redactHookBoundaryStrings(args),
	}

	// EDGE-016: run the mapper so agentd receives deterministic mapped/
	// redacted/hashed action fields. Failures are non-fatal — if the
	// mapper errors (e.g. on a future schema we haven't taught it about)
	// the agentd still receives the raw fields above and can fall back to
	// its own classification path.
	mapped, err := MapHookInput(input, mappingContextFromEnv(env))
	if err != nil {
		return req
	}
	req.Layer = string(mapped.Layer)
	req.Kind = string(mapped.Kind)
	req.TenantID = mapped.TenantID
	req.PrincipalID = mapped.PrincipalID
	req.Capability = mapped.Capability
	req.RiskTags = append([]string(nil), mapped.RiskTags...)
	req.Labels = mappedLabelsCopy(mapped.Labels)
	req.InputRedacted = mapped.InputRedacted
	req.InputHash = mapped.InputHash
	req.ActionHash = mapped.ActionHash
	req.ReasonCode = mapped.ReasonCode
	// Override SessionID/ExecutionID with the agentd-trusted values from
	// the mapping context. cordum-agentd sets CORDUM_EDGE_SESSION_ID/
	// EXECUTION_ID when it spawns the hook; whatever Claude reported in
	// the hook stdin is informational only.
	if mapped.SessionID != "" {
		req.SessionID = mapped.SessionID
	}
	if mapped.ExecutionID != "" {
		req.ExecutionID = mapped.ExecutionID
	}
	return req
}

func mappedLabelsCopy(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	maps.Copy(out, labels)
	return out
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
