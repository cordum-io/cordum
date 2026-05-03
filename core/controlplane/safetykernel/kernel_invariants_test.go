package safetykernel

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestKernelInvariants_DenyBlocksOtherwiseAllowedInput proves that an
// invariant DENY rule overrides a base ALLOW rule when both match the
// same topic — the security floor's first-match semantics holds across
// the kernel evaluator.
func TestKernelInvariants_DenyBlocksOtherwiseAllowedInput(t *testing.T) {
	srv := &server{}
	base := &config.SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{
			{
				ID:       "base-allow-job-test",
				Match:    config.PolicyMatch{Topics: []string{"job.test"}},
				Decision: "allow",
			},
		},
	}
	invariants := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{
				ID:       "inv-deny-job-test",
				Match:    config.PolicyMatch{Topics: []string{"job.test"}},
				Decision: "deny",
				Reason:   "SecOps invariant — job.test denied",
			},
		},
	}

	if err := srv.setPolicyWithInvariants(context.Background(), base, invariants, "cfg:test", 0); err != nil {
		t.Fatalf("setPolicyWithInvariants: %v", err)
	}

	resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{
		JobId:  "job-1",
		Topic:  "job.test",
		Tenant: "default",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected DENY (invariant must beat base ALLOW), got %v reason=%q rule=%q",
			resp.GetDecision(), resp.GetReason(), resp.GetRuleId())
	}
	if resp.GetRuleId() != "inv-deny-job-test" {
		t.Fatalf("expected invariant rule to fire first; got rule=%q", resp.GetRuleId())
	}
}

// TestKernelInvariants_AllowYieldsToBaseDeny proves that an invariant
// ALLOW does NOT silently override a base DENY — the kernel's first-match
// evaluator should hit the base DENY before the invariant ALLOW fallback.
func TestKernelInvariants_AllowYieldsToBaseDeny(t *testing.T) {
	srv := &server{}
	base := &config.SafetyPolicy{
		DefaultDecision: "allow",
		Rules: []config.PolicyRule{
			{
				ID:       "base-deny-job-test",
				Match:    config.PolicyMatch{Topics: []string{"job.test"}},
				Decision: "deny",
				Reason:   "explicit base deny",
			},
		},
	}
	invariants := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{
				ID:       "inv-allow-default",
				Match:    config.PolicyMatch{Topics: []string{"job.test"}},
				Decision: "allow",
			},
		},
	}

	if err := srv.setPolicyWithInvariants(context.Background(), base, invariants, "cfg:test", 0); err != nil {
		t.Fatalf("setPolicyWithInvariants: %v", err)
	}

	resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{
		JobId:  "job-2",
		Topic:  "job.test",
		Tenant: "default",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected DENY (base DENY beats invariant ALLOW); got %v", resp.GetDecision())
	}
	if resp.GetRuleId() != "base-deny-job-test" {
		t.Fatalf("expected base DENY to fire first; got rule=%q", resp.GetRuleId())
	}
}

// TestKernelInvariants_GlobalPolicyView confirms the kernel exposes a
// typed GlobalPolicy view that includes the invariants as a separate
// section while the kernel's evaluation policy bakes them into Rules.
func TestKernelInvariants_GlobalPolicyView(t *testing.T) {
	srv := &server{}
	base := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "base-allow", Match: config.PolicyMatch{Topics: []string{"job.test"}}, Decision: "allow"},
			{ID: "edge-deny", Match: config.PolicyMatch{Topics: []string{EdgeActionTopic}}, Decision: "deny"},
		},
	}
	invariants := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "inv-deny", Match: config.PolicyMatch{Topics: []string{"*"}}, Decision: "deny"},
		},
	}

	if err := srv.setPolicyWithInvariants(context.Background(), base, invariants, "cfg:view", 0); err != nil {
		t.Fatalf("setPolicyWithInvariants: %v", err)
	}

	g := srv.currentGlobalPolicy()
	if g == nil {
		t.Fatal("expected currentGlobalPolicy to return a view; got nil")
	}
	if g.SnapshotHash != "cfg:view" {
		t.Fatalf("expected SnapshotHash=cfg:view, got %q", g.SnapshotHash)
	}
	// Invariants live in their own section, NOT in InputRules — the
	// view is built from BASE merge so InputRules excludes invariants.
	if len(g.Invariants) != 1 || g.Invariants[0].ID != "inv-deny" {
		t.Fatalf("expected one invariant in view, got %+v", g.Invariants)
	}
	for _, r := range g.InputRules {
		if r.ID == "inv-deny" {
			t.Fatalf("invariant rule must NOT appear in InputRules section (would double-count); got %+v", g.InputRules)
		}
	}
	if len(g.EdgeActionRules) != 1 || g.EdgeActionRules[0].ID != "edge-deny" {
		t.Fatalf("expected edge-deny in EdgeActionRules section; got %+v", g.EdgeActionRules)
	}
}

// TestKernelInvariants_OutputDenyBlocksOtherwiseAllowed proves that an
// invariant OutputRule (quarantine) prepends to merged.OutputRules and
// fires before any base allow-mode output rule.
func TestKernelInvariants_OutputDenyBlocksOtherwiseAllowed(t *testing.T) {
	srv := &server{
		scanners: map[string]OutputScanner{},
	}
	base := &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "base-allow-output",
				Severity: "low",
				Match:    config.OutputPolicyMatch{Topics: []string{"job.report"}, Keywords: []string{"hello"}},
				Decision: "allow",
			},
		},
	}
	invariants := &config.SafetyPolicy{
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "inv-quarantine-secret",
				Severity: "critical",
				Match:    config.OutputPolicyMatch{Topics: []string{"job.report"}, Keywords: []string{"OPENAI_API_KEY="}},
				Decision: "quarantine",
				Reason:   "SecOps invariant — secret leak",
			},
		},
	}

	if err := srv.setPolicyWithInvariants(context.Background(), base, invariants, "cfg:out", 0); err != nil {
		t.Fatalf("setPolicyWithInvariants: %v", err)
	}

	resp, err := srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID:         "job-out-1",
		Topic:         "job.report",
		Tenant:        "default",
		OutputContent: []byte("OPENAI_API_KEY=sk-xyz"),
	})
	if err != nil {
		t.Fatalf("EvaluateOutput: %v", err)
	}
	if resp.Decision != "quarantine" {
		t.Fatalf("expected quarantine (invariant must fire on secret); got decision=%q rule=%q", resp.Decision, resp.RuleID)
	}
	if resp.RuleID != "inv-quarantine-secret" {
		t.Fatalf("expected invariant rule to fire first; got rule=%q", resp.RuleID)
	}
}
