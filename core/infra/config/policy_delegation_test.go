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
			name:  "direct call fails closed when max depth is set",
			match: &DelegationMatch{MaxDepth: &zero},
			want:  false,
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
			name:  "direct call fails closed when issuer allowlist is set",
			match: &DelegationMatch{Issuers: []string{"agent-a"}},
			want:  false,
		},
		{
			name:  "direct call fails closed when require issuer is set",
			match: &DelegationMatch{RequireIssuer: "finance-bot"},
			want:  false,
		},
		{
			name:  "direct call fails closed when required scope is set",
			match: &DelegationMatch{RequiredScope: []string{"read"}},
			want:  false,
		},
		{
			name:  "direct call admitted when delegation_required is explicitly false",
			match: &DelegationMatch{RequiredScope: []string{"read"}, DelegationRequired: boolPtr(false)},
			want:  true,
		},
		{
			name:  "direct call rejected when delegation_required is explicitly true",
			match: &DelegationMatch{DelegationRequired: boolPtr(true)},
			want:  false,
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
		LabelDelegationExpiresAt:    "2026-04-21T13:14:15Z",
		LabelDelegationAudience:     "agent-b",
	})
	if got == nil {
		t.Fatal("expected delegation context")
	}
	if got.Depth != 2 || got.RootIssuer != "agent-a" || got.ParentIssuer != "agent-b" || got.JTI != "dlg-123" || got.ExpiresAt != "2026-04-21T13:14:15Z" || got.Audience != "agent-b" {
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

// TestDelegationMatchDenyCallback asserts the observer callback fires with
// the expected `field` label for every sub-field that can short-circuit a
// rule — this is the seam safetykernel's metrics use for per-field
// Prometheus counters.
func TestDelegationMatchDenyCallback(t *testing.T) {
	zero := 0
	maxOne := 1

	type rejectedBy struct{ field string }
	tests := []struct {
		name       string
		match      *DelegationMatch
		delegation *DelegationContext
		wantField  string
	}{
		{
			name:       "forbid_delegated",
			match:      &DelegationMatch{ForbidDelegated: true},
			delegation: &DelegationContext{Depth: 1, IssuerChain: []string{"a"}},
			wantField:  "forbid_delegated",
		},
		{
			name:       "max_depth",
			match:      &DelegationMatch{MaxDepth: &zero},
			delegation: &DelegationContext{Depth: 1, IssuerChain: []string{"a"}},
			wantField:  "max_depth",
		},
		{
			name:       "issuers",
			match:      &DelegationMatch{Issuers: []string{"a"}},
			delegation: &DelegationContext{Depth: 1, IssuerChain: []string{"x"}},
			wantField:  "issuers",
		},
		{
			name:       "require_issuer",
			match:      &DelegationMatch{RequireIssuer: "a"},
			delegation: &DelegationContext{Depth: 1, RootIssuer: "b", IssuerChain: []string{"b"}},
			wantField:  "require_issuer",
		},
		{
			name:       "required_scope",
			match:      &DelegationMatch{RequiredScope: []string{"write"}},
			delegation: &DelegationContext{Depth: 1, Scope: []string{"read"}, IssuerChain: []string{"a"}, RootIssuer: "a"},
			wantField:  "required_scope",
		},
		{
			name:       "max_depth_multi_field_short_circuits_on_depth",
			match:      &DelegationMatch{MaxDepth: &maxOne, Issuers: []string{"a"}},
			delegation: &DelegationContext{Depth: 2, IssuerChain: []string{"a", "a"}},
			wantField:  "max_depth",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured []rejectedBy
			SetDelegationMatchDenyCallback(func(field string) {
				captured = append(captured, rejectedBy{field: field})
			})
			t.Cleanup(func() { SetDelegationMatchDenyCallback(nil) })

			if evaluateDelegationMatch(tc.match, tc.delegation) {
				t.Fatalf("evaluateDelegationMatch should have rejected")
			}
			if len(captured) != 1 {
				t.Fatalf("callback called %d times, want 1", len(captured))
			}
			if captured[0].field != tc.wantField {
				t.Fatalf("field = %q, want %q", captured[0].field, tc.wantField)
			}
		})
	}
}

// TestDelegationMatchDenyCallbackNotCalledOnMatch asserts the callback does
// NOT fire when a rule matches. Counting matches on the deny counter would
// balloon dashboards with noise.
func TestDelegationMatchDenyCallbackNotCalledOnMatch(t *testing.T) {
	var called int
	SetDelegationMatchDenyCallback(func(field string) { called++ })
	t.Cleanup(func() { SetDelegationMatchDenyCallback(nil) })

	// nil delegation passes every non-Forbid rule → no callback.
	_ = evaluateDelegationMatch(&DelegationMatch{MaxDepth: func() *int { i := 0; return &i }()}, nil)
	// nil match is neutral → no callback.
	_ = evaluateDelegationMatch(nil, &DelegationContext{Depth: 1})
	// Forbid + direct call is a match → no callback.
	_ = evaluateDelegationMatch(&DelegationMatch{ForbidDelegated: true}, nil)

	if called != 0 {
		t.Fatalf("callback fired %d times on matches; should be 0", called)
	}
}

// TestDelegationAuditExtras verifies the projection from DelegationContext
// to the SIEMEvent.Extra map. The scope list is deliberately omitted so a
// single safety-decision event stays under the 8 KiB syslog line limit.
func TestDelegationAuditExtras(t *testing.T) {
	if got := DelegationAuditExtras(nil); got != nil {
		t.Fatalf("DelegationAuditExtras(nil) = %v, want nil", got)
	}

	ctx := &DelegationContext{
		Depth:        2,
		IssuerChain:  []string{"finance-bot", "agent-a"},
		RootIssuer:   "finance-bot",
		ParentIssuer: "agent-a",
		Scope:        []string{"read", "write"}, // must NOT appear in Extras
		JTI:          "jti-123",
		ExpiresAt:    "2026-04-21T13:14:15Z",
		Audience:     "agent-a",
	}
	got := DelegationAuditExtras(ctx)
	wantPairs := map[string]string{
		"delegation.depth":         "2",
		"delegation.root_issuer":   "finance-bot",
		"delegation.parent_issuer": "agent-a",
		"delegation.jti":           "jti-123",
		"delegation.expires_at":    "2026-04-21T13:14:15Z",
		"delegation.audience":      "agent-a",
	}
	for k, v := range wantPairs {
		if got[k] != v {
			t.Errorf("Extras[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, present := got["delegation.scope"]; present {
		t.Fatalf("Extras must NOT include delegation.scope (syslog size rail)")
	}
	if got["delegation.depth"] == "" {
		t.Fatal("depth must always be emitted, even zero-valued")
	}
	if _, present := got["delegation.chain"]; present {
		t.Fatalf("Extras must NOT include delegation.chain; the companion lineage event carries the full chain")
	}
}

func TestDelegationAuditExtras_OmitsBlankFields(t *testing.T) {
	ctx := &DelegationContext{Depth: 0}
	got := DelegationAuditExtras(ctx)
	if got["delegation.depth"] != "0" {
		t.Fatalf("depth=0 should be emitted verbatim; got %q", got["delegation.depth"])
	}
	for _, key := range []string{"delegation.root_issuer", "delegation.parent_issuer", "delegation.jti", "delegation.expires_at", "delegation.audience", "delegation.chain"} {
		if _, ok := got[key]; ok {
			t.Errorf("key %q should be omitted when the source field is empty", key)
		}
	}
}

func TestDelegationAuditExtras_TrimsAudienceAndExpiry(t *testing.T) {
	ctx := &DelegationContext{Depth: 3, ExpiresAt: " 2026-04-21T13:14:15Z ", Audience: " agent-b "}
	got := DelegationAuditExtras(ctx)
	if got["delegation.expires_at"] != "2026-04-21T13:14:15Z" {
		t.Fatalf("expires_at should be trimmed; got %q", got["delegation.expires_at"])
	}
	if got["delegation.audience"] != "agent-b" {
		t.Fatalf("audience should be trimmed; got %q", got["delegation.audience"])
	}
}

// boolPtr is a local convenience for taking the address of a bool
// literal in test-case table entries. Keeps the Delegation matcher
// table readable without scattering `func() *bool { ... }` boilerplate.
func boolPtr(b bool) *bool { return &b }
