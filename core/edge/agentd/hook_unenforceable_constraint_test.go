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

func TestHookConstraintToolHelper(t *testing.T) {
	if path := os.Getenv("CORDUM_CONSTRAINT_MARKER"); path != "" {
		if err := os.WriteFile(path, []byte("started"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Only the hook permission can start the inert stand-in for a tool.
func hookConstraintRunTool(t *testing.T, decision claude.AgentdDecision) string {
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
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHookConstraintToolHelper$")
	cmd.Env = append(os.Environ(), "CORDUM_CONSTRAINT_MARKER="+marker)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tool helper: %v: %s", err, output)
	}
	return marker
}

func TestHookConstraintAllowsUnconstrainedTool(t *testing.T) {
	decision := AgentdDecisionFromEvaluateResponse(context.Background(), EvaluateResponse{Decision: "ALLOW"}, ApprovalDecisionConfig{}, nil, nil)
	if _, err := os.Stat(hookConstraintRunTool(t, decision)); err != nil {
		t.Fatalf("positive control did not start: %v", err)
	}
}

func TestHookConstraintDeniesUnsupportedKinds(t *testing.T) {
	for _, tc := range []struct{ kind, value string }{
		{"sandbox", `{"isolated":true}`},
		{"toolchain", `{"allowedCommands":["npm test"]}`},
		{"diff", `{"maxFiles":1}`},
		{"redactionLevel", `"strict"`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			var resp EvaluateResponse
			if err := json.Unmarshal([]byte(`{"decision":"CONSTRAIN","constraints":{"`+tc.kind+`":`+tc.value+`}}`), &resp); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"mapping", "fresh_evaluate"} {
				t.Run(path, func(t *testing.T) {
					decision := hookConstraintDecision(t, resp, path)
					if _, err := os.Stat(hookConstraintRunTool(t, decision)); !os.IsNotExist(err) {
						t.Fatalf("tool must not start for an unsupported constraint; stat=%v", err)
					}
					out := claude.ClaudeHookOutputForDecision("PreToolUse", decision)
					if out.HookSpecificOutput == nil || out.HookSpecificOutput.PermissionDecision != "deny" {
						t.Fatal("unsupported constraint must produce hook deny")
					}
				})
			}
		})
	}
}

func hookConstraintDecision(t *testing.T, resp EvaluateResponse, path string) claude.AgentdDecision {
	t.Helper()
	if path == "mapping" {
		return AgentdDecisionFromEvaluateResponse(context.Background(), resp, ApprovalDecisionConfig{}, nil, nil)
	}
	client := &stubEvaluateClient{resp: &resp}
	evaluator := NewEvaluator(EvaluatorConfig{Client: client, State: evaluatorTestState(edgecore.PolicyModeEnforce)})
	decision, err := evaluator.EvaluateHook(context.Background(), evaluatorMetricsRequest())
	if err != nil || len(client.requests) != 1 {
		t.Fatalf("fresh evaluate: err=%v calls=%d", err, len(client.requests))
	}
	return decision
}
