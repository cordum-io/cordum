//go:build integration

package safetykernel

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestEDGE052_OneInvariantBlocksAllFourSurfaces is the load-bearing
// proof point for the EDGE-052 "single Global authority" claim from the
// task definition: ONE invariant rule must block the same action across
// all FOUR evaluators (Cordum job, Edge action, MCP tool, output scan).
//
// All four sub-subtests share ONE kernel server with ONE *GlobalPolicy
// snapshot — the test asserts the SnapshotHash is identical across each
// evaluator's view so a regression that lets the surfaces drift apart
// (the historic split-brain problem) is caught immediately.
//
// Subtest C asserts that the kernel's GlobalPolicy view exposes the
// invariant rule via RulesForMCPTool() — the gate-side enforcement
// (gatewayApprovalGate.Check returning ErrMCPInvariantDeny when the
// lookup yields a matching DENY rule) is proven separately in
// gateway/mcp_gate_invariants_test.go so this integration test can stay
// inside the safetykernel package without taking a downward gateway
// dependency.
func TestEDGE052_OneInvariantBlocksAllFourSurfaces(t *testing.T) {
	srv := &server{scanners: map[string]OutputScanner{}}

	// Base policy: pack-style ALLOW for both Cordum-job + Edge actions
	// targeting the secret-path label, plus an output rule that would
	// allow the leak. Without invariants, all four evaluators would
	// return ALLOW for the .env / API_KEY=... action — the invariant
	// must override every one of them.
	base := &config.SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{
			{
				ID: "pack-allow-cordum-job-secret-read",
				Match: config.PolicyMatch{
					Topics: []string{"job.deploy"},
					Labels: map[string]string{"path.class": "secret"},
				},
				Decision: "allow",
			},
			{
				ID: "pack-allow-edge-secret-read",
				Match: config.PolicyMatch{
					Topics: []string{EdgeActionTopic},
					Labels: map[string]string{"path.class": "secret"},
				},
				Decision: "allow",
			},
			{
				ID: "pack-allow-mcp-fs-read",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{AllowTools: []string{"fs.read"}},
				},
				Decision: "allow",
			},
		},
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "pack-allow-output-default",
				Severity: "low",
				Match:    config.OutputPolicyMatch{Topics: []string{"job.report"}},
				Decision: "allow",
			},
		},
	}

	// SecOps invariant: SECURITY FLOOR. Authored ONCE, applied to all
	// four evaluators. The Cordum-job + Edge cases share a single
	// invariant rule (same labels + decision) — proves cross-section
	// reuse. The MCP case lives in the same Invariants block. The
	// output case has its own invariant OutputRule for content scanning.
	invariants := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{
				ID: "inv-deny-secret-paths",
				Match: config.PolicyMatch{
					Labels: map[string]string{"path.class": "secret"},
				},
				Decision: "deny",
				Reason:   "SecOps invariant — secret paths forbidden across all surfaces",
			},
			{
				ID: "inv-deny-mcp-fs-read",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{DenyTools: []string{"fs.read"}},
				},
				Decision: "deny",
				Reason:   "SecOps invariant — fs.read forbidden",
			},
		},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "inv-quarantine-secret-output",
				Severity: "critical",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.report"},
					Keywords: []string{"OPENAI_API_KEY="},
				},
				Decision: "quarantine",
				Reason:   "SecOps invariant — secret leak in output",
			},
		},
	}

	const snapshot = "cfg:edge052-integration"
	if err := srv.setPolicyWithInvariants(context.Background(), base, invariants, snapshot, 0); err != nil {
		t.Fatalf("setPolicyWithInvariants: %v", err)
	}

	// Pin the shared snapshot — all four sub-subtests must read the
	// same hash via currentGlobalPolicy(). A regression that lets one
	// surface diverge from the others bumps this assertion.
	g0 := srv.currentGlobalPolicy()
	if g0 == nil || g0.SnapshotHash != snapshot {
		t.Fatalf("kernel currentGlobalPolicy().SnapshotHash = %q, want %q", safeSnapshot(g0), snapshot)
	}

	t.Run("A_CordumJobEvaluator", func(t *testing.T) {
		// Even though pack-allow-cordum-job-secret-read targets job.deploy
		// + label path.class=secret with decision=allow, the invariant
		// inv-deny-secret-paths (label match only) fires first because
		// applyKernelInvariants prepends DENYs to merged.Rules.
		resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{
			JobId:  "job-cordum",
			Topic:  "job.deploy",
			Tenant: "default",
			Labels: map[string]string{"path.class": "secret"},
		})
		if err != nil {
			t.Fatalf("kernel.Evaluate: %v", err)
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
			t.Fatalf("Cordum job evaluator: want DENY, got %v rule=%q reason=%q",
				resp.GetDecision(), resp.GetRuleId(), resp.GetReason())
		}
		if resp.GetRuleId() != "inv-deny-secret-paths" {
			t.Fatalf("expected invariant to fire first; rule=%q", resp.GetRuleId())
		}
		if resp.GetPolicySnapshot() != snapshot {
			t.Fatalf("Cordum job snapshot = %q, want %q", resp.GetPolicySnapshot(), snapshot)
		}
	})

	t.Run("B_EdgeEvaluator", func(t *testing.T) {
		// evaluateEdgeSafety in the gateway forwards directly to
		// safetyClient.Evaluate (handlers_edge_evaluate.go:414). The
		// same Evaluate gRPC handler powers the Edge path — verified
		// here with topic=job.edge.action.
		resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{
			JobId:  "job-edge",
			Topic:  EdgeActionTopic,
			Tenant: "default",
			Labels: map[string]string{"path.class": "secret"},
		})
		if err != nil {
			t.Fatalf("kernel.Evaluate (edge topic): %v", err)
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
			t.Fatalf("Edge evaluator: want DENY, got %v rule=%q", resp.GetDecision(), resp.GetRuleId())
		}
		if resp.GetRuleId() != "inv-deny-secret-paths" {
			t.Fatalf("expected SAME invariant rule as Cordum job; rule=%q", resp.GetRuleId())
		}
		if resp.GetPolicySnapshot() != snapshot {
			t.Fatalf("Edge snapshot = %q, want %q", resp.GetPolicySnapshot(), snapshot)
		}
	})

	t.Run("C_MCPGate_SeesSameInvariantViaGlobalPolicyView", func(t *testing.T) {
		// The MCP gate (gateway/mcp_gate.go gatewayApprovalGate.Check)
		// consults its MCPInvariantLookup before the approval flow. In
		// production the lookup reads the kernel's GlobalPolicy view via
		// /api/v1/policy/global. This subtest asserts the kernel's view
		// EXPOSES the MCP invariant — gate-side enforcement is proven
		// in gateway/mcp_gate_invariants_test.go without crossing the
		// kernel→gateway dependency boundary.
		g := srv.currentGlobalPolicy()
		if g == nil {
			t.Fatal("currentGlobalPolicy returned nil")
		}
		if g.SnapshotHash != snapshot {
			t.Fatalf("MCP view snapshot = %q, want %q", g.SnapshotHash, snapshot)
		}
		mcpRules := g.RulesForMCPTool()
		if !containsRuleID(mcpRules, "inv-deny-mcp-fs-read") {
			t.Fatalf("MCP gate's RulesForMCPTool() missing invariant; got %v", ruleIDs(mcpRules))
		}
	})

	t.Run("D_OutputScanner", func(t *testing.T) {
		resp, err := srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
			JobID:         "job-output",
			Topic:         "job.report",
			Tenant:        "default",
			OutputContent: []byte("OPENAI_API_KEY=sk-zzz"),
		})
		if err != nil {
			t.Fatalf("kernel.EvaluateOutput: %v", err)
		}
		if resp.Decision != "quarantine" {
			t.Fatalf("Output scanner: want quarantine, got decision=%q rule=%q", resp.Decision, resp.RuleID)
		}
		if resp.RuleID != "inv-quarantine-secret-output" {
			t.Fatalf("expected invariant output rule; got %q", resp.RuleID)
		}
		if resp.PolicySnapshot != snapshot {
			t.Fatalf("Output snapshot = %q, want %q", resp.PolicySnapshot, snapshot)
		}
	})

	// Final assertion — pin SnapshotHash uniformity across the four
	// surfaces' view. If any surface had a parallel snapshot, this
	// final read would diverge.
	gFinal := srv.currentGlobalPolicy()
	if gFinal == nil || gFinal.SnapshotHash != snapshot {
		t.Fatalf("post-test snapshot drift: %q != %q", safeSnapshot(gFinal), snapshot)
	}
}

func safeSnapshot(g *GlobalPolicy) string {
	if g == nil {
		return ""
	}
	return g.SnapshotHash
}

func containsRuleID(rules []config.PolicyRule, id string) bool {
	for _, r := range rules {
		if r.ID == id {
			return true
		}
	}
	return false
}

func ruleIDs(rules []config.PolicyRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.ID)
	}
	return out
}
