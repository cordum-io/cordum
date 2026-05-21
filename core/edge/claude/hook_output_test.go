package claude

import (
	"encoding/json"
	"strings"
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

func TestPreToolUseOutputRedactsUpdatedInput(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE"
	out := ClaudeHookOutputForDecision("PreToolUse", AgentdDecision{
		Decision: DecisionAsk,
		Reason:   "gateway constrained input",
		UpdatedInput: map[string]any{
			"command": "echo " + secret,
			"env":     map[string]any{"AWS_ACCESS_KEY_ID": secret},
		},
	})
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	encoded := string(data)
	if strings.Contains(encoded, secret) {
		t.Fatalf("updatedInput leaked secret to Claude stdout JSON: %s", encoded)
	}
	if !strings.Contains(encoded, "redacted") {
		t.Fatalf("updatedInput did not include redaction marker: %s", encoded)
	}
}
