package claude

import "fmt"

type ClaudeHookOutput struct {
	Continue           *bool               `json:"continue,omitempty"`
	StopReason         string              `json:"stopReason,omitempty"`
	SuppressOutput     *bool               `json:"suppressOutput,omitempty"`
	SystemMessage      string              `json:"systemMessage,omitempty"`
	Decision           string              `json:"decision,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type HookSpecificOutput struct {
	HookEventName            string         `json:"hookEventName"`
	PermissionDecision       string         `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
	AdditionalContext        string         `json:"additionalContext,omitempty"`
}

func ClaudeHookOutputForDecision(eventName string, d AgentdDecision) ClaudeHookOutput {
	switch eventName {
	case "PreToolUse":
		return preToolUseOutput(d)
	case "UserPromptSubmit":
		return userPromptSubmitOutput(d)
	case "PostToolUse", "PostToolUseFailure":
		return postToolUseOutput(eventName, d)
	case "ConfigChange":
		return configChangeOutput(d)
	case "FileChanged":
		return ClaudeHookOutput{}
	default:
		return ClaudeHookOutput{}
	}
}

func preToolUseOutput(d AgentdDecision) ClaudeHookOutput {
	permission := string(d.Decision)
	reason := d.Reason
	switch d.Decision {
	case DecisionAllow, DecisionDeny, DecisionAsk:
		// Use as-is.
	case DecisionRequireApproval:
		permission = string(DecisionDeny)
		if reason == "" {
			reason = "approval required"
		}
		if d.ApprovalRef != "" {
			reason = fmt.Sprintf("%s; approval_ref=%s; approve then retry the tool call", reason, d.ApprovalRef)
		}
	default:
		return ClaudeHookOutput{}
	}
	out := ClaudeHookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       permission,
		PermissionDecisionReason: reason,
		UpdatedInput:             redactHookBoundaryMap(d.UpdatedInput),
		AdditionalContext:        d.AdditionalContext,
	}}
	// Also set the top-level `decision: "block"` mirror that every other
	// hook output function in this file already does for deny/require_approval
	// (see userPromptSubmitOutput, postToolUseOutput, configChangeOutput).
	// End-to-end Edge testing on 2026-05-28 showed that Claude Code v2.1.x
	// will silently proceed with the tool call when only the inner
	// permissionDecision is set — it treats that as a soft ask, which in
	// non-interactive `-p` mode is effectively allow. Setting BOTH fields
	// is the canonical "hard block" shape Claude Code respects across
	// interactive and headless invocations. PreToolUse was the odd one
	// out; this brings it in line with the rest of the hook events.
	if permission == string(DecisionDeny) {
		out.Decision = "block"
		if out.Reason == "" {
			out.Reason = reason
		}
	}
	return out
}

func userPromptSubmitOutput(d AgentdDecision) ClaudeHookOutput {
	out := ClaudeHookOutput{HookSpecificOutput: &HookSpecificOutput{HookEventName: "UserPromptSubmit"}}
	if d.AdditionalContext != "" {
		out.HookSpecificOutput.AdditionalContext = d.AdditionalContext
	}
	switch d.Decision {
	case DecisionDeny, DecisionRequireApproval:
		out.Decision = "block"
		out.Reason = d.Reason
	case DecisionAllow:
		if d.AdditionalContext == "" {
			return ClaudeHookOutput{}
		}
	default:
		return ClaudeHookOutput{}
	}
	return out
}

func postToolUseOutput(eventName string, d AgentdDecision) ClaudeHookOutput {
	out := ClaudeHookOutput{HookSpecificOutput: &HookSpecificOutput{HookEventName: eventName}}
	if d.AdditionalContext != "" {
		out.HookSpecificOutput.AdditionalContext = d.AdditionalContext
	}
	switch d.Decision {
	case DecisionDeny, DecisionRequireApproval:
		out.Decision = "block"
		out.Reason = d.Reason
	case DecisionAllow:
		if d.AdditionalContext == "" {
			return ClaudeHookOutput{}
		}
	default:
		return ClaudeHookOutput{}
	}
	return out
}

func configChangeOutput(d AgentdDecision) ClaudeHookOutput {
	out := ClaudeHookOutput{HookSpecificOutput: &HookSpecificOutput{HookEventName: "ConfigChange"}}
	switch d.Decision {
	case DecisionDeny, DecisionRequireApproval:
		out.Decision = "block"
		if d.Reason != "" {
			out.Reason = d.Reason
		} else {
			out.Reason = "configuration change blocked by Cordum policy"
		}
	case DecisionAllow:
		return ClaudeHookOutput{}
	default:
		return ClaudeHookOutput{}
	}
	return out
}
