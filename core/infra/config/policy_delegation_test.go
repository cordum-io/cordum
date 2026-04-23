package config

import "testing"

func TestSafetyPolicyEvaluate_DelegationDepthPredicate(t *testing.T) {
	policy := &SafetyPolicy{
		DefaultDecision: "allow",
		Rules: []PolicyRule{
			{
				ID:       "deny-deep-delegation",
				Decision: "deny",
				Reason:   "delegation too deep",
				Match: PolicyMatch{
					Predicate: "delegation.depth > 2",
				},
			},
		},
	}

	decision := policy.Evaluate(PolicyInput{
		Tenant: "default",
		Topic:  "job.test",
		Delegation: &DelegationContext{
			Depth: 3,
		},
	})
	if decision.Decision != "deny" {
		t.Fatalf("decision = %q, want deny", decision.Decision)
	}
	if decision.RuleID != "deny-deep-delegation" {
		t.Fatalf("rule = %q, want deny-deep-delegation", decision.RuleID)
	}
}

func TestSafetyPolicyEvaluate_DelegationScopeContainsPredicate(t *testing.T) {
	policy := &SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []PolicyRule{
			{
				ID:       "allow-delegated-read",
				Decision: "allow",
				Reason:   "delegated read permitted",
				Match: PolicyMatch{
					Predicate: "delegation.scope.contains('read')",
				},
			},
		},
	}

	decision := policy.Evaluate(PolicyInput{
		Tenant: "default",
		Topic:  "job.test",
		Delegation: &DelegationContext{
			Depth: 1,
			Scope: []string{"read", "summarize"},
		},
	})
	if decision.Decision != "allow" {
		t.Fatalf("decision = %q, want allow", decision.Decision)
	}
}

func TestSafetyPolicyEvaluate_NilDelegationDoesNotMatchPredicates(t *testing.T) {
	tests := []struct {
		name      string
		predicate string
	}{
		{name: "equals_zero", predicate: "delegation.depth == 0"},
		{name: "greater_than_zero", predicate: "delegation.depth > 0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := &SafetyPolicy{
				DefaultDecision: "allow",
				Rules: []PolicyRule{
					{
						ID:       tc.name,
						Decision: "deny",
						Reason:   "predicate matched",
						Match: PolicyMatch{
							Predicate: tc.predicate,
						},
					},
				},
			}

			decision := policy.Evaluate(PolicyInput{
				Tenant: "default",
				Topic:  "job.test",
			})
			if decision.Decision != "allow" {
				t.Fatalf("decision = %q, want allow when delegation is absent", decision.Decision)
			}
		})
	}
}
