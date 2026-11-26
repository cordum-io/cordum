package agentd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

func TestBuildCommandToolHelper(t *testing.T) {
	if path := os.Getenv("CORDUM_BUILD_COMMAND_MARKER"); path != "" {
		if err := os.WriteFile(path, []byte("started"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func buildCommandRunTool(t *testing.T, decision claude.AgentdDecision) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "started")
	out := claude.ClaudeHookOutputForDecision("PreToolUse", decision)
	if out.HookSpecificOutput == nil {
		t.Fatal("tool permission must be explicit")
	}
	permission := out.HookSpecificOutput.PermissionDecision
	if permission != "allow" && permission != "deny" {
		t.Fatalf("unexpected tool permission: %q", permission)
	}
	if out.Decision == "block" || permission == "deny" {
		return marker
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBuildCommandToolHelper$")
	cmd.Env = append(os.Environ(), "CORDUM_BUILD_COMMAND_MARKER="+marker)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tool helper: %v: %s", err, output)
	}
	return marker
}

func buildCommandRequest(t *testing.T, command string) claude.AgentdRequest {
	t.Helper()
	encoded, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	var input claude.HookInput
	if err := json.Unmarshal([]byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":`+string(encoded)+`}}`), &input); err != nil {
		t.Fatal(err)
	}
	mapped, err := claude.MapHookInput(input, claude.MappingContext{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production command class: %s", mapped.Labels["command.class"])
	return claude.AgentdRequest{
		EventName: input.HookEventName, ToolName: input.ToolName, ToolInput: input.ToolInput,
		Layer: string(mapped.Layer), Kind: string(mapped.Kind), Capability: mapped.Capability,
		InputRedacted: mapped.InputRedacted, InputHash: mapped.InputHash, ActionHash: mapped.ActionHash,
		RiskTags: mapped.RiskTags, Labels: mapped.Labels,
	}
}

func TestBuildCommandRequiresAvailableGateway(t *testing.T) {
	for _, command := range []string{"go test ./...", "npm test", "make test"} {
		t.Run(command, func(t *testing.T) {
			client := &stubEvaluateClient{err: ErrGatewayTimeout}
			evaluator := NewEvaluator(EvaluatorConfig{Client: client, State: evaluatorTestState(edgecore.PolicyModeEnforce)})
			decision, err := evaluator.EvaluateHook(context.Background(), buildCommandRequest(t, command))
			if err != nil || len(client.requests) != 1 {
				t.Fatalf("evaluate: err=%v calls=%d", err, len(client.requests))
			}
			if _, err := os.Stat(buildCommandRunTool(t, decision)); !os.IsNotExist(err) {
				t.Fatalf("repository command must not start without gateway authority; stat=%v", err)
			}
		})
	}
}

func TestBuildCommandStartsWithGatewayAllow(t *testing.T) {
	client := &stubEvaluateClient{resp: &EvaluateResponse{Decision: "ALLOW"}}
	evaluator := NewEvaluator(EvaluatorConfig{Client: client, State: evaluatorTestState(edgecore.PolicyModeEnforce)})
	decision, err := evaluator.EvaluateHook(context.Background(), buildCommandRequest(t, "npm test"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(buildCommandRunTool(t, decision)); err != nil {
		t.Fatalf("positive control did not start: %v", err)
	}
}
