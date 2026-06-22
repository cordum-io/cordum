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
	// Compute the hook output once, derive the metric/audit-recorded
	// decision from it. Previously this fired recordHookObservability with
	// the raw `decision.Decision` BEFORE hookOutputForRun synthesized a deny
	// for the degraded+enforce case — so a fail-closed PreToolUse showed
	// up in metrics as the original `RECORDED` decision with degraded=false,
	// even though the hook actually emitted a block. CodeRabbit on #319
	// caught this; recording after synthesis keeps metrics honest.
	out := hookOutputForRun(input.HookEventName, decision, opts)
	effectiveDecision := decision.Decision
	degraded := decision.Degraded
	failClosed := false
	reasonCode := hookDenyReasonCode(req, "")
	if input.HookEventName == "PreToolUse" && decision.Degraded && enforcesOnDegraded(opts) {
		effectiveDecision = DecisionDeny
		degraded = true
		failClosed = true
		reasonCode = "degraded_policy_enforced"
	}
	recordHookObservability(opts, req, effectiveDecision, reasonCode, degraded, failClosed, startedAt)
	return writeRunOutputComputed(stderr, stdout, out)
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

// writeRunOutputComputed is the shared write tail. Run() computes
// hookOutputForRun upstream so it can derive the observability decision
// from the synthesis path; handleAgentdError and the fail-closed paths go
// through here too. Keeping the empty-check and the writeJSON error
// handling in one place means no caller has to remember the contract.
func writeRunOutputComputed(stderr, stdout io.Writer, out ClaudeHookOutput) int {
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
	// enforce + local-dev-enforce both degrade-CLOSED for risky / unknown
	// PreToolUse actions. cordumctl edge doctor documents this contract
	// ("enforce degrades closed for risky/unknown actions"), but until this
	// fix only local-dev-enforce was wired up — `enforce` silently fell
	// through to the catch-all `return 0` below (0 bytes of stdout, exit 0
	// = Claude proceeds). End-to-end traces caught Write going through in
	// policy_mode=enforce when agentd was unreachable (post EOF).
	if (localDevEnforce(opts) || enforceMode(opts)) && input.HookEventName == "PreToolUse" {
		// Preserve the "local enforcer" wording for local-dev-enforce so
		// downstream monitors matching on the exact string keep firing;
		// production enforce gets the unqualified phrasing.
		enforcerLabel := "Cordum Edge enforcer"
		if localDevEnforce(opts) {
			enforcerLabel = "Cordum Edge local enforcer"
		}
		localReason := enforcerLabel + " unavailable; blocking unclassified action"
		if riskyPreToolUse(input) {
			localReason = enforcerLabel + " unavailable; blocking risky action"
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
	// The launcher sets both CORDUM_EDGE_MODE and CORDUM_EDGE_POLICY_MODE
	// (see launcher_metadata.go); honor either so the hook reads the same
	// value the operator passed via --policy-mode regardless of which env
	// var survives a downstream wrapper.
	if mode := strings.TrimSpace(envValue(opts.Env, "CORDUM_EDGE_POLICY_MODE")); mode != "" {
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

// enforceMode reports whether the active policy mode is "enforce" — the
// production-shape mode where risky / unknown PreToolUse actions must
// fail closed when agentd is unreachable. Distinct from failClosed
// (which is the strictest enterprise-strict deny-everything-on-degraded)
// and from localDevEnforce (which targets the dev workstation
// explicitly). Reads both CORDUM_EDGE_POLICY_MODE and CORDUM_EDGE_MODE
// because the launcher sets both (launcher_metadata.go); a downstream
// wrapper that drops one of them shouldn't silently downgrade the
// fail-mode posture.
func enforceMode(opts RunOptions) bool {
	if mode := strings.ToLower(strings.TrimSpace(envValue(opts.Env, "CORDUM_EDGE_POLICY_MODE"))); mode == "enforce" {
		return true
	}
	return strings.EqualFold(envValue(opts.Env, "CORDUM_EDGE_MODE"), "enforce")
}

// fileMutationTools is the static set of Claude Code tools whose contract
// includes writing or deleting on the operator's filesystem. Each of
// these is risky-by-default for PreToolUse fail-closed gating — the
// safety-kernel rules in cordum-edge-pack treat them as file.write
// capability and the demo's claude-code.require-approval-for-edits rule
// gates exactly this set. Kept as a map for O(1) lookup; case matters
// because Claude Code tool names are PascalCase.
var fileMutationTools = map[string]struct{}{
	"Write":        {},
	"Edit":         {},
	"MultiEdit":    {},
	"NotebookEdit": {},
}

func riskyPreToolUse(input HookInput) bool {
	// Tool-less or unknown invocation — fail closed to surface the gap.
	if input.ToolName == "" {
		return true
	}
	// File-mutation tools are always risky for PreToolUse: their contract
	// is "modify the filesystem". Previously this function only flagged
	// Bash + destructive shell, which let Write / Edit / MultiEdit /
	// NotebookEdit slip through enforcer-unavailable paths even though
	// the demo policy pack's require-approval-for-edits rule clearly
	// intends to gate them.
	if _, ok := fileMutationTools[input.ToolName]; ok {
		return true
	}
	if !strings.EqualFold(input.ToolName, "Bash") {
		return false
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
	case "PreToolUse":
		// When agentd flagged the response as degraded (gateway evaluation
		// could not complete in time and a RECORDED placeholder came back
		// instead of a real allow/deny/require_approval), the previous code
		// fell through to ClaudeHookOutputForDecision → preToolUseOutput's
		// `default:` case, which returns an empty output → Claude proceeds.
		// In policy_mode=enforce / enterprise-strict that is a silent
		// fail-OPEN on every risky tool call whenever the safety kernel
		// is briefly slow. Fail-CLOSE instead: synthesize a deny with a
		// reason that names the degraded state so the audit trail and the
		// model both see why the call was blocked.
		if decision.Degraded && enforcesOnDegraded(opts) {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "Cordum Edge evaluation not ready; blocking under policy_mode=" + hookPolicyMode(opts)
			} else {
				reason = "Cordum Edge evaluation degraded (" + reason + "); blocking under policy_mode=" + hookPolicyMode(opts)
			}
			return ClaudeHookOutputForDecision(eventName, AgentdDecision{
				Decision: DecisionDeny,
				Reason:   reason,
			})
		}
		return ClaudeHookOutputForDecision(eventName, decision)
	default:
		return ClaudeHookOutputForDecision(eventName, decision)
	}
}

// enforcesOnDegraded reports whether the active policy mode requires the
// hook to treat a degraded agentd response as a deny. Today: enforce and
// enterprise-strict do; observe does not (it just records). Centralized
// here so the mode set stays in one place and future modes don't have to
// touch hookOutputForRun.
func enforcesOnDegraded(opts RunOptions) bool {
	switch strings.ToLower(strings.TrimSpace(hookPolicyMode(opts))) {
	case string(edgecore.PolicyModeEnforce),
		string(edgecore.PolicyModeEnterpriseStrict):
		return true
	default:
		return false
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
