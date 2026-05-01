package claude

import (
	"encoding/json"
	"testing"
)

// TestMapEdgeDecisionToHookOutputPreToolUseAllow asserts uppercase EDGE-009
// ALLOW maps to Claude permissionDecision="allow" with the carried reason
// and no extra deny noise.
func TestMapEdgeDecisionToHookOutputPreToolUseAllow(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("PreToolUse", EdgeDecisionResponse{
		Decision: "ALLOW",
		Reason:   "Test commands are allowed",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	assertCompactJSON(t, marshalOutput(t, out), `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"Test commands are allowed"}}`)
}

func TestMapEdgeDecisionToHookOutputPreToolUseDeny(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("PreToolUse", EdgeDecisionResponse{
		Decision: "DENY",
		Reason:   "Reading .env is blocked by Cordum policy",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	assertCompactJSON(t, marshalOutput(t, out), `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Reading .env is blocked by Cordum policy"}}`)
}

func TestMapEdgeDecisionToHookOutputPreToolUseRequireApprovalIsImmediateDenyWithApprovalRef(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("PreToolUse", EdgeDecisionResponse{
		Decision:    "REQUIRE_APPROVAL",
		Reason:      "Editing production deployment files requires approval",
		ApprovalRef: "edge_appr_01J7QY",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	assertCompactJSON(t, marshalOutput(t, out), `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Editing production deployment files requires approval; approval_ref=edge_appr_01J7QY; approve then retry the tool call"}}`)
}

func TestMapEdgeDecisionToHookOutputPreToolUseThrottleIsDenyWithReason(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("PreToolUse", EdgeDecisionResponse{
		Decision: "THROTTLE",
		Reason:   "Rate limited; try again shortly",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	hsop := out.HookSpecificOutput
	if hsop == nil || hsop.PermissionDecision != "deny" {
		t.Fatalf("THROTTLE did not map to deny; out=%+v", out)
	}
	if hsop.PermissionDecisionReason == "" {
		t.Fatalf("THROTTLE deny missing reason: %+v", hsop)
	}
}

func TestMapEdgeDecisionToHookOutputPreToolUseConstrainAppliesUpdatedInput(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("PreToolUse", EdgeDecisionResponse{
		Decision:     "CONSTRAIN",
		Reason:       "Constrained to a safer command",
		UpdatedInput: map[string]any{"command": "go test -count=1 ./core/edge"},
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	hsop := out.HookSpecificOutput
	if hsop == nil {
		t.Fatalf("CONSTRAIN missing HookSpecificOutput; out=%+v", out)
	}
	if hsop.PermissionDecision != "allow" {
		t.Errorf("CONSTRAIN PermissionDecision = %q, want allow", hsop.PermissionDecision)
	}
	if got, _ := hsop.UpdatedInput["command"].(string); got != "go test -count=1 ./core/edge" {
		t.Errorf("UpdatedInput[command] = %q, want safer test command", got)
	}
}

func TestMapEdgeDecisionToHookOutputPreToolUseConstrainWithoutUpdatedInputFallsBackToDeny(t *testing.T) {
	// CONSTRAIN without an updated input is unsafe to apply (we'd have to
	// invent a command); fail closed by mapping to deny+reason.
	out, err := MapEdgeDecisionToHookOutput("PreToolUse", EdgeDecisionResponse{
		Decision: "CONSTRAIN",
		Reason:   "policy says constrain but no safe alternative was provided",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	hsop := out.HookSpecificOutput
	if hsop == nil || hsop.PermissionDecision != "deny" {
		t.Fatalf("CONSTRAIN without UpdatedInput must deny; out=%+v", out)
	}
}

func TestMapEdgeDecisionToHookOutputUserPromptSubmitDenyBlocksWithReason(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("UserPromptSubmit", EdgeDecisionResponse{
		Decision: "DENY",
		Reason:   "Prompt blocked by tenant policy",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	if out.Decision != "block" {
		t.Errorf("UserPromptSubmit DENY top-level Decision = %q, want block", out.Decision)
	}
	if out.Reason == "" {
		t.Errorf("UserPromptSubmit block missing reason; out=%+v", out)
	}
}

func TestMapEdgeDecisionToHookOutputUserPromptSubmitAllowWithAdditionalContext(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("UserPromptSubmit", EdgeDecisionResponse{
		Decision:          "ALLOW",
		AdditionalContext: "synthetic context",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	hsop := out.HookSpecificOutput
	if hsop == nil || hsop.AdditionalContext != "synthetic context" {
		t.Fatalf("UserPromptSubmit ALLOW with context did not produce additionalContext: %+v", out)
	}
	if out.Decision == "block" {
		t.Errorf("ALLOW must not produce block; out=%+v", out)
	}
}

func TestMapEdgeDecisionToHookOutputUserPromptSubmitAllowEmptyIsNoOp(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("UserPromptSubmit", EdgeDecisionResponse{Decision: "ALLOW"})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	if !isEmptyOutput(out) {
		t.Errorf("ALLOW with no context must produce empty output (no-op); out=%+v", out)
	}
}

func TestMapEdgeDecisionToHookOutputPostToolUseBlockDoesNotClaimPrevention(t *testing.T) {
	// PostToolUse fires AFTER the tool ran. A "block" here is feedback for
	// the model, not prevention of execution. The mapper must not synthesize
	// a permission_decision (which is PreToolUse-only) on PostToolUse output.
	out, err := MapEdgeDecisionToHookOutput("PostToolUse", EdgeDecisionResponse{
		Decision: "DENY",
		Reason:   "Tool result rejected",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	if out.Decision != "block" {
		t.Errorf("PostToolUse DENY top-level Decision = %q, want block", out.Decision)
	}
	if out.HookSpecificOutput != nil && out.HookSpecificOutput.PermissionDecision != "" {
		t.Errorf("PostToolUse must not set PermissionDecision (PreToolUse-only field); got %+v", out.HookSpecificOutput)
	}
}

func TestMapEdgeDecisionToHookOutputPostToolUseFailureBlock(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("PostToolUseFailure", EdgeDecisionResponse{
		Decision: "DENY",
		Reason:   "tool failure pattern blocked",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	if out.Decision != "block" {
		t.Errorf("PostToolUseFailure DENY Decision = %q, want block", out.Decision)
	}
}

func TestMapEdgeDecisionToHookOutputUnknownEventIsNoOp(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("MysteryHook", EdgeDecisionResponse{
		Decision: "DENY",
		Reason:   "n/a",
	})
	if err != nil {
		t.Fatalf("MapEdgeDecisionToHookOutput: %v", err)
	}
	if !isEmptyOutput(out) {
		t.Errorf("Unknown event must produce empty output; got %+v", out)
	}
}

func TestMapEdgeDecisionToHookOutputInvalidDecisionReturnsErrorAndEmptyOutput(t *testing.T) {
	out, err := MapEdgeDecisionToHookOutput("PreToolUse", EdgeDecisionResponse{
		Decision: "BANANA",
		Reason:   "what is this",
	})
	if err == nil {
		t.Fatal("MapEdgeDecisionToHookOutput on invalid decision returned nil error")
	}
	if !isEmptyOutput(out) {
		t.Errorf("Invalid decision must produce empty output; got %+v", out)
	}
}

func TestMapEdgeDecisionToHookOutputLowercaseDecisionsAlsoAccepted(t *testing.T) {
	// Defensive: agentd may pass through lowercase legacy AgentdDecision
	// values. Mapper should accept both upper and lowercase to keep
	// runner/agentd mapping simple.
	out, err := MapEdgeDecisionToHookOutput("PreToolUse", EdgeDecisionResponse{
		Decision: "deny",
		Reason:   "lowercase legacy",
	})
	if err != nil {
		t.Fatalf("lowercase decision errored: %v", err)
	}
	hsop := out.HookSpecificOutput
	if hsop == nil || hsop.PermissionDecision != "deny" {
		t.Errorf("lowercase deny did not map to permissionDecision=deny; got %+v", out)
	}
}

func marshalOutput(t *testing.T, out ClaudeHookOutput) string {
	t.Helper()
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}
