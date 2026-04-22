package safetykernel

import (
	"context"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// delegationLabels builds the reserved _delegation.* label set that the
// gateway writes after token verification. The kernel's
// delegationContextFromRequest helper reconstructs a DelegationContext
// from these labels.
func delegationLabels(depth string, chain, scope []string, root, parent, jti string) map[string]string {
	labels := map[string]string{}
	if depth != "" {
		labels[config.LabelDelegationDepth] = depth
	}
	if len(chain) > 0 {
		labels[config.LabelDelegationIssuerChain] = strings.Join(chain, ",")
	}
	if len(scope) > 0 {
		labels[config.LabelDelegationScope] = strings.Join(scope, ",")
	}
	if root != "" {
		labels[config.LabelDelegationIssuer] = root
	}
	if parent != "" {
		labels[config.LabelDelegationParentIssuer] = parent
	}
	if jti != "" {
		labels[config.LabelDelegationJTI] = jti
	}
	return labels
}

// withDelegationEnabled flips CORDUM_DELEGATION_POLICY_ENABLED to true for
// the duration of the test. The gateway->kernel wire uses this env gate to
// keep the feature dark until operators explicitly enable it.
func withDelegationEnabled(t *testing.T) {
	t.Helper()
	t.Setenv(envDelegationPolicyEnabled, "true")
}

// delegationPolicy returns a SafetyPolicy that combines every DelegationMatch
// sub-field into a single rule set, so every DoD scenario can be exercised
// against the same kernel instance.
func delegationPolicy() *config.SafetyPolicy {
	maxDepth2 := 2
	maxDepth1 := 1
	return &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "allow",
		Rules: []config.PolicyRule{
			{
				ID:       "deny-direct-calls-to-sensitive",
				Decision: "deny",
				Reason:   "direct calls forbidden on sensitive topic",
				Match: config.PolicyMatch{
					Topics:     []string{"job.sensitive.direct-only"},
					Delegation: &config.DelegationMatch{ForbidDelegated: true},
				},
			},
			{
				// Allow-envelope: rule matches only when chain ≤ 2
				// AND every chain member is in allowlist AND root is
				// finance-bot AND scope ⊇ [read]. Non-matching delegated
				// jobs fall through to the default "allow" — a realistic
				// policy would fail-closed, but we use the permissive
				// default here to make the negative cases observable.
				ID:       "allow-finance-reads",
				Decision: "allow",
				Reason:   "finance-bot-rooted read chain within envelope",
				Match: config.PolicyMatch{
					Topics: []string{"job.sensitive.finance-read"},
					Delegation: &config.DelegationMatch{
						MaxDepth:      &maxDepth2,
						Issuers:       []string{"finance-bot", "analyst-bot"},
						RequireIssuer: "finance-bot",
						RequiredScope: []string{"read"},
					},
				},
			},
			{
				// Deny-any-deep rule as a fall-closed backstop — rule fires
				// when delegation is nil (direct call is allowed to match any
				// rule) OR chain ≤ 1. Combined with a deny decision, this
				// lets an operator cap chains at depth=1.
				ID:       "deny-shallow-chains-to-restricted",
				Decision: "deny",
				Reason:   "restricted topic chain depth",
				Match: config.PolicyMatch{
					Topics:     []string{"job.sensitive.depth-capped"},
					Delegation: &config.DelegationMatch{MaxDepth: &maxDepth1},
				},
			},
		},
	}
}

// TestKernelDelegation_ForbidDelegated covers DoD scenario:
// "No delegation = depth 0 (direct call), passes all delegation rules."
// Direct calls to a forbid_delegated=true rule match → deny. Delegated
// calls to the same rule do NOT match → fall through to default allow.
func TestKernelDelegation_ForbidDelegated(t *testing.T) {
	withDelegationEnabled(t)
	srv, _ := newTestServerWithVelocity(t, delegationPolicy(), "snap-delegation-forbid")

	t.Run("direct_call_denied", func(t *testing.T) {
		resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
			JobId:  "job-direct",
			Topic:  "job.sensitive.direct-only",
			Tenant: "default",
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
			t.Fatalf("decision = %v, want DENY (direct call against forbid_delegated rule)", resp.GetDecision())
		}
	})

	t.Run("delegated_call_falls_through_to_allow", func(t *testing.T) {
		labels := delegationLabels("1", []string{"agent-a"}, []string{"read"}, "agent-a", "agent-a", "jti-1")
		resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
			JobId:  "job-delegated",
			Topic:  "job.sensitive.direct-only",
			Tenant: "default",
			Labels: labels,
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
			t.Fatalf("decision = %v, want ALLOW (forbid rule does not match delegated)", resp.GetDecision())
		}
	})
}

// TestKernelDelegation_MultiFieldEnvelope exercises the combined
// MaxDepth + Issuers + RequireIssuer + RequiredScope match block against
// multiple chain shapes.
func TestKernelDelegation_MultiFieldEnvelope(t *testing.T) {
	withDelegationEnabled(t)
	srv, _ := newTestServerWithVelocity(t, delegationPolicy(), "snap-delegation-envelope")

	tests := []struct {
		name   string
		labels map[string]string
		// the allow-finance-reads rule fires when everything matches; a
		// non-match falls through to default "allow" as well, so we
		// distinguish via the returned reason/rule id.
		wantRuleID string
	}{
		{
			name:       "happy_path_finance_rooted",
			labels:     delegationLabels("2", []string{"finance-bot", "analyst-bot"}, []string{"read", "write"}, "finance-bot", "analyst-bot", "jti-finance"),
			wantRuleID: "allow-finance-reads",
		},
		{
			name:       "chain_too_deep_falls_through",
			labels:     delegationLabels("3", []string{"finance-bot", "analyst-bot", "analyst-bot"}, []string{"read"}, "finance-bot", "analyst-bot", "jti-deep"),
			wantRuleID: "", // fall through to default "allow"
		},
		{
			name:       "issuer_off_allowlist_falls_through",
			labels:     delegationLabels("2", []string{"finance-bot", "rogue-bot"}, []string{"read"}, "finance-bot", "rogue-bot", "jti-rogue"),
			wantRuleID: "",
		},
		{
			name:       "wrong_root_issuer_falls_through",
			labels:     delegationLabels("2", []string{"analyst-bot", "finance-bot"}, []string{"read"}, "analyst-bot", "finance-bot", "jti-wrong-root"),
			wantRuleID: "",
		},
		{
			name:       "missing_scope_falls_through",
			labels:     delegationLabels("1", []string{"finance-bot"}, []string{"write"}, "finance-bot", "finance-bot", "jti-noscope"),
			wantRuleID: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
				JobId:  "job-" + tc.name,
				Topic:  "job.sensitive.finance-read",
				Tenant: "default",
				Labels: tc.labels,
			})
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if resp.GetRuleId() != tc.wantRuleID {
				t.Fatalf("ruleId = %q, want %q", resp.GetRuleId(), tc.wantRuleID)
			}
		})
	}
}

// TestKernelDelegation_MaxDepthCapFallClosed covers a deny rule with
// MaxDepth=1 under the new fail-closed matcher semantic. The rule now
// only matches delegated requests (because MaxDepth is a delegation-
// scoped constraint). Direct calls NO LONGER match the rule and fall
// through to the default allow — closing the old foot-gun where a
// deny-on-depth rule intended for delegated traffic also caught direct
// calls. Chains at MaxDepth are still denied; chains past MaxDepth fall
// through as before.
func TestKernelDelegation_MaxDepthCapFallClosed(t *testing.T) {
	withDelegationEnabled(t)
	srv, _ := newTestServerWithVelocity(t, delegationPolicy(), "snap-delegation-cap")

	t.Run("direct_call_falls_through_to_allow", func(t *testing.T) {
		resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
			JobId:  "job-direct-cap",
			Topic:  "job.sensitive.depth-capped",
			Tenant: "default",
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		// After the fail-closed matcher fix, a deny rule scoped to
		// delegated traffic (MaxDepth set) does NOT match direct calls.
		// The direct call falls through to the default-allow decision.
		if resp.GetDecision() == pb.DecisionType_DECISION_TYPE_DENY {
			t.Fatalf("decision = DENY; fail-closed matcher should let direct calls fall through a delegation-scoped deny rule")
		}
	})

	t.Run("chain_depth_1_denied", func(t *testing.T) {
		labels := delegationLabels("1", []string{"agent-a"}, []string{"read"}, "agent-a", "agent-a", "jti-d1")
		resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
			JobId:  "job-d1-cap",
			Topic:  "job.sensitive.depth-capped",
			Tenant: "default",
			Labels: labels,
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
			t.Fatalf("decision = %v, want DENY (depth 1 within envelope)", resp.GetDecision())
		}
	})

	t.Run("chain_depth_3_falls_through", func(t *testing.T) {
		labels := delegationLabels("3", []string{"a", "b", "c"}, []string{"read"}, "a", "c", "jti-d3")
		resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
			JobId:  "job-d3-cap",
			Topic:  "job.sensitive.depth-capped",
			Tenant: "default",
			Labels: labels,
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
			t.Fatalf("decision = %v, want ALLOW (depth 3 escapes max=1 envelope, falls through to default)", resp.GetDecision())
		}
	})
}

// TestKernelDelegation_DisabledByEnvGate pins the kill switch behavior —
// when CORDUM_DELEGATION_POLICY_ENABLED is unset, the kernel ignores the
// _delegation.* labels entirely. This gives operators a rollback lever
// during incident response without re-deploying a policy bundle.
func TestKernelDelegation_DisabledByEnvGate(t *testing.T) {
	// Deliberately do NOT call withDelegationEnabled.
	srv, _ := newTestServerWithVelocity(t, delegationPolicy(), "snap-delegation-disabled")

	labels := delegationLabels("1", []string{"agent-a"}, []string{"read"}, "agent-a", "agent-a", "jti-disabled")
	resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
		JobId:  "job-disabled",
		Topic:  "job.sensitive.direct-only",
		Tenant: "default",
		Labels: labels,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Without the env gate, delegationContextFromRequest returns nil, so the
	// ForbidDelegated rule sees this as a direct call → denies.
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("decision = %v, want DENY (env gate off disables delegation awareness)", resp.GetDecision())
	}
}

// TestKernelDelegation_MetricIncrementsOnDeny asserts the observability
// seam from step 3 survives end-to-end: a chain-depth-denied evaluation
// increments the safety_rule_delegation_match_total counter.
func TestKernelDelegation_MetricIncrementsOnDeny(t *testing.T) {
	withDelegationEnabled(t)

	var seen []string
	config.SetDelegationMatchDenyCallback(func(field string) { seen = append(seen, field) })
	t.Cleanup(func() {
		// Restore the real Prometheus-backed observer so other tests in the
		// package continue to see metric increments.
		config.SetDelegationMatchDenyCallback(func(field string) {
			safetyRuleDelegationMatchTotal.WithLabelValues(field, "deny").Inc()
		})
	})

	srv, _ := newTestServerWithVelocity(t, delegationPolicy(), "snap-delegation-metric")

	// Chain depth 3 on a max-depth-1 rule → the rule's match fails via
	// max_depth. But because the rule is a "deny shallow chains" rule and
	// the chain is NOT shallow, the match returns false → rule does not
	// fire. The callback still records the max_depth deny — which is
	// exactly the signal operators need to see.
	labels := delegationLabels("3", []string{"a", "b", "c"}, []string{"read"}, "a", "c", "jti-metric")
	_, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
		JobId:  "job-metric",
		Topic:  "job.sensitive.depth-capped",
		Tenant: "default",
		Labels: labels,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	foundMaxDepth := false
	for _, f := range seen {
		if f == "max_depth" {
			foundMaxDepth = true
			break
		}
	}
	if !foundMaxDepth {
		t.Fatalf("expected max_depth deny observation; got %v", seen)
	}
}
