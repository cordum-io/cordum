package claude

import (
	"encoding/json"
	"testing"
)

func TestPreToolUseOutputOmitsEmptyOptionalFields(t *testing.T) {
	out := ClaudeHookOutputForDecision("PreToolUse", AgentdDecision{Decision: DecisionDeny, Reason: "blocked"})
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	assertCompactJSON(t, string(data), `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"blocked"}}`)
}

func TestRequireApprovalMapsToImmediateDenyWithApprovalRef(t *testing.T) {
	out := ClaudeHookOutputForDecision("PreToolUse", AgentdDecision{Decision: DecisionRequireApproval, Reason: "approval required", ApprovalRef: "edge_appr_123"})
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	assertCompactJSON(t, string(data), `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"approval required; approval_ref=edge_appr_123; approve then retry the tool call"}}`)
}

func TestPreToolUseAskIncludesUpdatedInput(t *testing.T) {
	out := ClaudeHookOutputForDecision("PreToolUse", AgentdDecision{
		Decision:     DecisionAsk,
		Reason:       "needs operator confirmation",
		UpdatedInput: map[string]any{"command": "npm test -- --runInBand"},
	})
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	assertCompactJSON(t, string(data), `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"needs operator confirmation","updatedInput":{"command":"npm test -- --runInBand"}}}`)
}
