package config

import (
	"strings"
	"testing"
)

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

func TestEvaluateDelegationMatch(t *testing.T) {
	zero := 0
	one := 1
	tests := []struct {
		name       string
		match      *DelegationMatch
		delegation *DelegationContext
		want       bool
	}{
		{
			name:  "nil match is neutral",
			match: nil,
			want:  true,
		},
		{
			name:  "forbid delegated allows direct call",
			match: &DelegationMatch{ForbidDelegated: true},
			want:  true,
		},
		{
			name:  "forbid delegated rejects delegated call",
			match: &DelegationMatch{ForbidDelegated: true},
			delegation: &DelegationContext{
				Depth:       1,
				IssuerChain: []string{"agent-a"},
				RootIssuer:  "agent-a",
			},
			want: false,
		},
		{
			name:  "direct call bypasses max depth",
			match: &DelegationMatch{MaxDepth: &zero},
			want:  true,
		},
		{
			name:  "max depth rejects deeper chain",
			match: &DelegationMatch{MaxDepth: &zero},
			delegation: &DelegationContext{
				Depth:       1,
				IssuerChain: []string{"agent-a"},
				RootIssuer:  "agent-a",
			},
			want: false,
		},
		{
			name:  "direct call bypasses issuer allowlist",
			match: &DelegationMatch{Issuers: []string{"agent-a"}},
			want:  true,
		},
		{
			name:  "direct call bypasses require issuer",
			match: &DelegationMatch{RequireIssuer: "finance-bot"},
			want:  true,
		},
		{
			name:  "direct call bypasses required scope",
			match: &DelegationMatch{RequiredScope: []string{"read"}},
			want:  true,
		},
		{
			name:  "issuer allowlist accepts every chain member",
			match: &DelegationMatch{Issuers: []string{"agent-a", "agent-b"}},
			delegation: &DelegationContext{
				Depth:       2,
				IssuerChain: []string{"agent-a", "agent-b"},
				RootIssuer:  "agent-a",
			},
			want: true,
		},
		{
			name:  "issuer allowlist rejects unknown chain member",
			match: &DelegationMatch{Issuers: []string{"agent-a", "agent-b"}},
			delegation: &DelegationContext{
				Depth:       2,
				IssuerChain: []string{"agent-a", "agent-x"},
				RootIssuer:  "agent-a",
			},
			want: false,
		},
		{
			name:  "require issuer matches root",
			match: &DelegationMatch{RequireIssuer: "finance-bot"},
			delegation: &DelegationContext{
				Depth:       1,
				IssuerChain: []string{"finance-bot"},
				RootIssuer:  "finance-bot",
			},
			want: true,
		},
		{
			name:  "require issuer rejects different root",
			match: &DelegationMatch{RequireIssuer: "finance-bot"},
			delegation: &DelegationContext{
				Depth:       1,
				IssuerChain: []string{"agent-a"},
				RootIssuer:  "agent-a",
			},
			want: false,
		},
		{
			name:  "required scope ignores order",
			match: &DelegationMatch{RequiredScope: []string{"read", "write"}},
			delegation: &DelegationContext{
				Depth:       1,
				IssuerChain: []string{"agent-a"},
				Scope:       []string{"write", "read"},
				RootIssuer:  "agent-a",
			},
			want: true,
		},
		{
			name:  "required scope rejects missing action",
			match: &DelegationMatch{RequiredScope: []string{"read", "write"}},
			delegation: &DelegationContext{
				Depth:       1,
				IssuerChain: []string{"agent-a"},
				Scope:       []string{"read"},
				RootIssuer:  "agent-a",
			},
			want: false,
		},
		{
			name: "multi field rule requires every condition",
			match: &DelegationMatch{
				MaxDepth:      &one,
				Issuers:       []string{"agent-a", "agent-b"},
				RequireIssuer: "agent-a",
				RequiredScope: []string{"read"},
			},
			delegation: &DelegationContext{
				Depth:       1,
				IssuerChain: []string{"agent-a", "agent-b"},
				Scope:       []string{"read", "write"},
				RootIssuer:  "agent-a",
			},
			want: true,
		},
		{
			name: "multi field rule fails if any condition fails",
			match: &DelegationMatch{
				MaxDepth:      &one,
				Issuers:       []string{"agent-a", "agent-b"},
				RequireIssuer: "agent-a",
				RequiredScope: []string{"read"},
			},
			delegation: &DelegationContext{
				Depth:       2,
				IssuerChain: []string{"agent-a", "agent-b"},
				Scope:       []string{"read"},
				RootIssuer:  "agent-a",
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := evaluateDelegationMatch(tc.match, tc.delegation); got != tc.want {
				t.Fatalf("evaluateDelegationMatch() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDelegationContextFromLabels(t *testing.T) {
	if got := DelegationContextFromLabels(nil); got != nil {
		t.Fatalf("DelegationContextFromLabels(nil) = %#v, want nil", got)
	}
	if got := DelegationContextFromLabels(map[string]string{}); got != nil {
		t.Fatalf("DelegationContextFromLabels(empty) = %#v, want nil", got)
	}
	if got := DelegationContextFromLabels(map[string]string{LabelDelegationDepth: "0"}); got != nil {
		t.Fatalf("DelegationContextFromLabels(depth=0) = %#v, want nil", got)
	}

	got := DelegationContextFromLabels(map[string]string{
		LabelDelegationDepth:        "2",
		LabelDelegationIssuerChain:  "agent-a,agent-b",
		LabelDelegationIssuer:       "agent-a",
		LabelDelegationParentIssuer: "agent-b",
		LabelDelegationScope:        "read,write",
		LabelDelegationJTI:          "dlg-123",
	})
	if got == nil {
		t.Fatal("expected delegation context")
	}
	if got.Depth != 2 || got.RootIssuer != "agent-a" || got.ParentIssuer != "agent-b" || got.JTI != "dlg-123" {
		t.Fatalf("unexpected delegation context: %#v", got)
	}
}

func TestParseSafetyPolicy_DelegationValidation(t *testing.T) {
	validYAML := []byte(`
default_decision: allow
rules:
  - id: delegation-allowlist
    decision: deny
    match:
      delegation:
        max_depth: 2
        issuers: [agent-a, agent-b]
        require_issuer: finance-bot
        required_scope: [read, write]
        forbid_delegated: false
`)
	policy, err := ParseSafetyPolicy(validYAML)
	if err != nil {
		t.Fatalf("ParseSafetyPolicy(valid) error = %v", err)
	}
	if policy == nil || policy.Rules[0].Match.Delegation == nil {
		t.Fatalf("expected delegation match to parse, got %#v", policy)
	}

	invalidCases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "negative-max-depth",
			yaml: `
default_decision: allow
rules:
  - id: bad-depth
    decision: deny
    match:
      delegation:
        max_depth: -1
`,
			wantErr: "max_depth",
		},
		{
			name: "duplicate-issuers",
			yaml: `
default_decision: allow
rules:
  - id: dup-issuers
    decision: deny
    match:
      delegation:
        issuers: [agent-a, agent-a]
`,
			wantErr: "duplicate",
		},
		{
			name: "invalid-require-issuer",
			yaml: `
default_decision: allow
rules:
  - id: bad-root
    decision: deny
    match:
      delegation:
        require_issuer: "not valid"
`,
			wantErr: "require_issuer",
		},
		{
			name: "empty-required-scope-entry",
			yaml: `
default_decision: allow
rules:
  - id: bad-scope
    decision: deny
    match:
      delegation:
        required_scope: [read, ""]
`,
			wantErr: "required_scope",
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSafetyPolicy([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ParseSafetyPolicy() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}
