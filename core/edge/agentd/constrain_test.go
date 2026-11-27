package agentd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

const constrainToolMarkerEnv = "CORDUM_CONSTRAIN_TOOL_MARKER"

// TestConstrainToolHelper is the stand-in tool. Run as a child process with
// constrainToolMarkerEnv set, it records that it started.
func TestConstrainToolHelper(t *testing.T) {
	if path := os.Getenv(constrainToolMarkerEnv); path != "" {
		if err := os.WriteFile(path, []byte("started"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// constrainShimStartsTool maps decision to PreToolUse hook output, starts the
// stand-in tool only when that output permits the call, and reports whether
// the tool started.
func constrainShimStartsTool(t *testing.T, decision claude.AgentdDecision) bool {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "tool-started")
	out := claude.ClaudeHookOutputForDecision("PreToolUse", decision)
	if out.Decision != "block" && out.HookSpecificOutput != nil && out.HookSpecificOutput.PermissionDecision == "allow" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConstrainToolHelper$")
		cmd.Env = append(os.Environ(), constrainToolMarkerEnv+"="+marker)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tool helper: %v: %s", err, output)
		}
	}
	_, err := os.Stat(marker)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
	return err == nil
}

func TestConstrainShimStartsToolOnAllow(t *testing.T) {
	evaluator := NewEvaluator(EvaluatorConfig{
		Client: &stubEvaluateClient{resp: &EvaluateResponse{Decision: string(edgecore.DecisionAllow)}},
		State:  evaluatorTestState(edgecore.PolicyModeEnforce),
	})
	decision, err := evaluator.EvaluateHook(context.Background(), evaluatorMetricsRequest())
	if err != nil {
		t.Fatalf("EvaluateHook: %v", err)
	}
	if !constrainShimStartsTool(t, decision) {
		t.Fatalf("decision %+v did not start the tool", decision)
	}
}

func TestEvaluatorConstrainUnenforceableKindDenies(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		value any
	}{
		{"sandbox", map[string]any{"isolated": true}},
		{"toolchain", map[string]any{"allowedCommands": []any{"npm test"}}},
		{"diff", map[string]any{"maxFiles": float64(1)}},
		{"redactionLevel", "strict"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			writer := &captureEventWriter{}
			evaluator := NewEvaluator(EvaluatorConfig{
				Client: &stubEvaluateClient{resp: &EvaluateResponse{
					Decision:                 string(edgecore.DecisionConstrain),
					Reason:                   "allowed with constraints",
					PermissionDecision:       "allow",
					PermissionDecisionReason: "allowed with constraints",
					Constraints:              map[string]any{tc.kind: tc.value},
					UpdatedInput:             map[string]any{"command": "npm test"},
				}},
				EventWriter: writer,
				State:       evaluatorTestState(edgecore.PolicyModeEnforce),
			})
			decision, err := evaluator.EvaluateHook(context.Background(), evaluatorMetricsRequest())
			if err != nil {
				t.Fatalf("EvaluateHook: %v", err)
			}
			want := edgecore.UnenforceableConstraintReason([]string{tc.kind})
			if decision.Decision != claude.DecisionDeny || decision.Reason != want || decision.UpdatedInput != nil {
				t.Fatalf("decision = %+v, want deny with reason %q and no updated input", decision, want)
			}
			if len(writer.events) != 1 || writer.events[0].Decision != edgecore.DecisionDeny || writer.events[0].DecisionReason != want {
				t.Fatalf("events = %+v, want one DENY event with reason %q", writer.events, want)
			}
			if constrainShimStartsTool(t, decision) {
				t.Fatal("the tool started for a constraint kind the hook cannot apply")
			}
		})
	}
}

// The safe-allow cache drops constraints, so an ALLOW that carries them must
// never be cached.
func TestEvaluatorAllowWithConstraintsDeniesUncached(t *testing.T) {
	client := &stubEvaluateClient{resp: &EvaluateResponse{
		Decision:           string(edgecore.DecisionAllow),
		PermissionDecision: "allow",
		CacheEligible:      true,
		PolicySnapshot:     "snap-eval",
		Constraints:        map[string]any{"sandbox": map[string]any{"isolated": true}},
	}}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client: client,
		State:  evaluatorTestState(edgecore.PolicyModeEnforce),
		Cache:  NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 4}, nil),
	})
	for i := range 2 {
		decision, err := evaluator.EvaluateHook(context.Background(), evaluatorMetricsRequest())
		if err != nil || decision.Decision != claude.DecisionDeny {
			t.Fatalf("call %d: decision = %+v, err = %v; want deny", i+1, decision, err)
		}
	}
	if len(client.requests) != 2 {
		t.Fatalf("gateway calls = %d, want 2: the second call must not be served from the cache", len(client.requests))
	}
}

// The Gateway answers a consumed approval with ALLOW and keeps the rule's
// constraints, so both ALLOW and CONSTRAIN are checked.
func TestConsumedApprovalConstrainUnenforceableKindDenies(t *testing.T) {
	cfg := ApprovalDecisionConfig{InlineWaitEnabled: true, InlineWaitTimeout: time.Second, PolicyMode: edgecore.PolicyModeEnforce}
	for _, tc := range []struct {
		name string
		resp EvaluateResponse
		kind string
	}{
		{"allow", EvaluateResponse{
			Decision:                 string(edgecore.DecisionAllow),
			Reason:                   "approval edge_appr_123 consumed",
			PermissionDecisionReason: "approval edge_appr_123 consumed",
			ApprovalRef:              "edge_appr_123",
			Constraints:              map[string]any{"sandbox": map[string]any{"isolated": true}},
		}, "sandbox"},
		{"constrain", EvaluateResponse{
			Decision:     string(edgecore.DecisionConstrain),
			Constraints:  map[string]any{"toolchain": map[string]any{"allowedCommands": []any{"npm test"}}},
			UpdatedInput: map[string]any{"command": "npm test"},
		}, "toolchain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			waiter := &fakeApprovalWaiter{result: ApprovalWaitResult{Status: ApprovalWaitApproved, UpdatedInput: map[string]any{"command": "npm test"}}}
			consume := func(context.Context, string) (*EvaluateResponse, error) { return &tc.resp, nil }
			decision := AgentdDecisionFromEvaluateResponse(context.Background(), approvalRequiredResponse(), cfg, waiter, consume)
			want := edgecore.UnenforceableConstraintReason([]string{tc.kind})
			if decision.Decision != claude.DecisionDeny || decision.Reason != want || decision.UpdatedInput != nil {
				t.Fatalf("decision = %+v, want deny with reason %q and no updated input", decision, want)
			}
			if constrainShimStartsTool(t, decision) {
				t.Fatal("the tool started for a constraint kind the hook cannot apply")
			}
		})
	}
}
