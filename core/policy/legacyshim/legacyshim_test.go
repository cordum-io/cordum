package legacyshim_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
	"github.com/cordum/cordum/core/policy/legacyshim"
)

func TestInputPolicyRuleRoundTrip(t *testing.T) {
	enabled := true
	cases := []struct {
		name string
		old  config.InputPolicyRule
	}{
		{
			name: "global tier with full match",
			old: config.InputPolicyRule{
				ID:       "block-secrets",
				Tier:     "global",
				Selector: config.PolicySelector{},
				Enabled:  &enabled,
				Severity: "critical",
				Desc:     "Block secret leaks in inputs",
				Match: config.InputPolicyMatch{
					Tenants:         []string{"acme"},
					Topics:          []string{"job.acme.*"},
					Capabilities:    []string{"llm.request"},
					RiskTags:        []string{"untrusted_input"},
					Scanners:        []string{"secret_leak"},
					ContentPatterns: []string{`(?i)api[_-]?key`},
					Keywords:        []string{"secret", "token"},
					ContentTypes:    []string{"application/json"},
					Detectors:       []string{"secret_leak"},
					InputSizeGt:     1024,
					MaxInputBytes:   1048576,
				},
				Decision: "deny",
				Reason:   "secret_leak_detected",
			},
		},
		{
			name: "workflow tier with selector",
			old: config.InputPolicyRule{
				ID:       "wf-claims-block",
				Tier:     "workflow",
				Selector: config.PolicySelector{WorkflowID: "wf-claims"},
				Severity: "high",
				Desc:     "Workflow-scoped block",
				Match:    config.InputPolicyMatch{Tenants: []string{"acme"}},
				Decision: "require_approval",
				Reason:   "workflow_governance",
			},
		},
		{
			name: "minimal global with empty match",
			old: config.InputPolicyRule{
				ID:       "minimal",
				Tier:     "global",
				Severity: "low",
				Desc:     "minimal",
				Match:    config.InputPolicyMatch{},
				Decision: "deny",
				Reason:   "default",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule, err := legacyshim.InputPolicyRuleToRule(tc.old)
			if err != nil {
				t.Fatalf("InputPolicyRuleToRule err = %v", err)
			}
			if rule.Type != policy.RuleTypeInput {
				t.Errorf("rule.Type = %q, want %q", rule.Type, policy.RuleTypeInput)
			}
			if rule.ID != tc.old.ID {
				t.Errorf("rule.ID = %q, want %q", rule.ID, tc.old.ID)
			}
			back, err := legacyshim.RuleToInputPolicyRule(rule)
			if err != nil {
				t.Fatalf("RuleToInputPolicyRule err = %v", err)
			}
			if !reflect.DeepEqual(tc.old, back) {
				t.Errorf("round-trip mismatch\nbefore: %+v\nafter:  %+v", tc.old, back)
			}
		})
	}
}

func TestOutputPolicyRuleRoundTrip(t *testing.T) {
	enabled := true
	hasError := false
	cases := []struct {
		name string
		old  config.OutputPolicyRule
	}{
		{
			name: "redact pii",
			old: config.OutputPolicyRule{
				ID:       "pii-redact",
				Enabled:  &enabled,
				Severity: "high",
				Desc:     "Redact PII in outputs",
				Match: config.OutputPolicyMatch{
					Tenants:         []string{"acme"},
					Topics:          []string{"job.acme.*"},
					Capabilities:    []string{"llm.request"},
					RiskTags:        []string{"contains_pii"},
					Scanners:        []string{"pii"},
					ContentPatterns: []string{`(?i)\bSSN\b`},
					Keywords:        []string{"social", "ssn"},
					ContentTypes:    []string{"application/json"},
					Detectors:       []string{"pii"},
					MaxOutputBytes:  4194304,
					HasError:        &hasError,
				},
				Decision: "redact",
				Reason:   "pii_in_output",
			},
		},
		{
			name: "quarantine on error",
			old: config.OutputPolicyRule{
				ID:       "err-quarantine",
				Severity: "medium",
				Desc:     "Quarantine error outputs",
				Match:    config.OutputPolicyMatch{},
				Decision: "quarantine",
				Reason:   "error_present",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule, err := legacyshim.OutputPolicyRuleToRule(tc.old)
			if err != nil {
				t.Fatalf("OutputPolicyRuleToRule err = %v", err)
			}
			if rule.Type != policy.RuleTypeOutput {
				t.Errorf("rule.Type = %q, want %q", rule.Type, policy.RuleTypeOutput)
			}
			back, err := legacyshim.RuleToOutputPolicyRule(rule)
			if err != nil {
				t.Fatalf("RuleToOutputPolicyRule err = %v", err)
			}
			if !reflect.DeepEqual(tc.old, back) {
				t.Errorf("round-trip mismatch\nbefore: %+v\nafter:  %+v", tc.old, back)
			}
		})
	}
}

func TestVelocityRuleRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		old  config.PolicyRule
	}{
		{
			name: "throttle on tenant",
			old: config.PolicyRule{
				ID:       "throttle-tenant",
				Tier:     "global",
				Selector: config.PolicySelector{},
				Match:    config.PolicyMatch{Tenants: []string{"acme"}},
				Velocity: &config.VelocityConfig{
					MaxRequests:   100,
					WindowSeconds: 60,
					Key:           "tenant",
				},
				Decision: "throttle",
				Reason:   "rate_limit",
			},
		},
		{
			name: "workflow throttle with constraints",
			old: config.PolicyRule{
				ID:       "wf-throttle",
				Tier:     "workflow",
				Selector: config.PolicySelector{WorkflowID: "wf-claims"},
				Match:    config.PolicyMatch{Tenants: []string{"acme"}, Topics: []string{"job.acme.claims"}},
				Velocity: &config.VelocityConfig{
					MaxRequests:   10,
					WindowSeconds: 30,
					Key:           "tenant:topic",
				},
				Decision: "allow_with_constraints",
				Reason:   "throttle_with_remediation",
				Constraints: config.PolicyConstraints{
					Budgets:        config.BudgetConstraints{MaxRuntimeMs: 5000},
					RedactionLevel: "standard",
				},
				Remediations: []config.PolicyRemediation{{
					ID:               "rem-1",
					Title:            "Use cached result",
					ReplacementTopic: "job.acme.claims.cached",
				}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule, err := legacyshim.PolicyRuleToVelocityRule(tc.old)
			if err != nil {
				t.Fatalf("PolicyRuleToVelocityRule err = %v", err)
			}
			if rule.Type != policy.RuleTypeVelocity {
				t.Errorf("rule.Type = %q, want %q", rule.Type, policy.RuleTypeVelocity)
			}
			back, err := legacyshim.RuleToPolicyRule(rule)
			if err != nil {
				t.Fatalf("RuleToPolicyRule err = %v", err)
			}
			if !reflect.DeepEqual(tc.old, back) {
				t.Errorf("round-trip mismatch\nbefore: %+v\nafter:  %+v", tc.old, back)
			}
		})
	}
}

func TestPolicyRuleToVelocityRuleRejectsNilVelocity(t *testing.T) {
	old := config.PolicyRule{
		ID:       "no-velocity",
		Match:    config.PolicyMatch{Tenants: []string{"acme"}},
		Decision: "deny",
	}
	_, err := legacyshim.PolicyRuleToVelocityRule(old)
	if err == nil {
		t.Fatal("expected error for nil Velocity, got nil")
	}
}

func TestRuleToInputPolicyRuleRejectsTypeMismatch(t *testing.T) {
	rule := policy.Rule{ID: "wrong", Type: policy.RuleTypeOutput}
	_, err := legacyshim.RuleToInputPolicyRule(rule)
	if !errors.Is(err, legacyshim.ErrRuleTypeMismatch) {
		t.Fatalf("expected ErrRuleTypeMismatch, got %v", err)
	}
}

func TestRuleToOutputPolicyRuleRejectsTypeMismatch(t *testing.T) {
	rule := policy.Rule{ID: "wrong", Type: policy.RuleTypeInput}
	_, err := legacyshim.RuleToOutputPolicyRule(rule)
	if !errors.Is(err, legacyshim.ErrRuleTypeMismatch) {
		t.Fatalf("expected ErrRuleTypeMismatch, got %v", err)
	}
}

func TestRuleToPolicyRuleRejectsTypeMismatch(t *testing.T) {
	rule := policy.Rule{ID: "wrong", Type: policy.RuleTypeInput}
	_, err := legacyshim.RuleToPolicyRule(rule)
	if !errors.Is(err, legacyshim.ErrRuleTypeMismatch) {
		t.Fatalf("expected ErrRuleTypeMismatch, got %v", err)
	}
}

func TestEdgePolicyModeToBundleMetadata(t *testing.T) {
	cases := []struct {
		in   string
		want policy.EdgeMode
	}{
		{"observe", policy.EdgeModeObserve},
		{"enforce", policy.EdgeModeEnforce},
		{"enterprise-strict", policy.EdgeModeEnterpriseStrict},
		{"  observe  ", policy.EdgeModeObserve},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := legacyshim.EdgePolicyModeToBundleMetadata(tc.in)
			if got.EdgeMode != tc.want {
				t.Errorf("EdgePolicyModeToBundleMetadata(%q).EdgeMode = %q, want %q", tc.in, got.EdgeMode, tc.want)
			}
		})
	}
}

func TestEdgeDecisionRoundTrip(t *testing.T) {
	cases := []struct {
		ed     edge.EdgeDecision
		dt     policy.DecisionType
		marker string
	}{
		{edge.DecisionAllow, policy.DecisionAllow, ""},
		{edge.DecisionDeny, policy.DecisionDeny, ""},
		{edge.DecisionRequireApproval, policy.DecisionRequireHuman, ""},
		{edge.DecisionThrottle, policy.DecisionThrottle, ""},
		{edge.DecisionConstrain, policy.DecisionAllowWithConstraints, ""},
		{edge.DecisionRecorded, policy.DecisionAllow, "recorded"},
	}
	for _, tc := range cases {
		t.Run(string(tc.ed), func(t *testing.T) {
			dt, err := legacyshim.EdgeDecisionToDecisionType(tc.ed)
			if err != nil {
				t.Fatalf("EdgeDecisionToDecisionType(%q) err = %v", tc.ed, err)
			}
			if dt != tc.dt {
				t.Errorf("EdgeDecisionToDecisionType(%q) = %q, want %q", tc.ed, dt, tc.dt)
			}
			d := policy.Decision{Type: dt}
			if tc.marker != "" {
				d.Trace = []policy.TraceStep{{DecisionType: dt, Reason: tc.marker}}
			}
			back, err := legacyshim.EdgeDecisionFromUnified(d)
			if err != nil {
				t.Fatalf("EdgeDecisionFromUnified err = %v", err)
			}
			if back != tc.ed {
				t.Errorf("EdgeDecisionFromUnified = %q, want %q", back, tc.ed)
			}
		})
	}
}

func TestEdgeDecisionToDecisionTypeRejectsUnknown(t *testing.T) {
	_, err := legacyshim.EdgeDecisionToDecisionType(edge.EdgeDecision("BOGUS"))
	if !errors.Is(err, legacyshim.ErrUnknownEdgeDecision) {
		t.Fatalf("expected ErrUnknownEdgeDecision, got %v", err)
	}
}

func TestEdgeDecisionFromUnifiedRejectsUnknown(t *testing.T) {
	d := policy.Decision{Type: policy.DecisionType("nonsense")}
	_, err := legacyshim.EdgeDecisionFromUnified(d)
	if !errors.Is(err, legacyshim.ErrUnknownDecisionType) {
		t.Fatalf("expected ErrUnknownDecisionType, got %v", err)
	}
}

func TestLegacyPolicyDecisionRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		pd   config.PolicyDecision
	}{
		{
			name: "deny with reason",
			pd: config.PolicyDecision{
				Decision: "deny",
				Reason:   "secret_leak",
				RuleID:   "block-secrets",
				RuleTier: "global",
			},
		},
		{
			name: "require_approval sets ApprovalRequired",
			pd: config.PolicyDecision{
				Decision:         "require_approval",
				Reason:           "needs_human",
				RuleID:           "approval-rule",
				RuleTier:         "workflow",
				ApprovalRequired: true,
			},
		},
		{
			name: "allow_with_constraints carries constraints",
			pd: config.PolicyDecision{
				Decision: "allow_with_constraints",
				Reason:   "constrain_resources",
				RuleID:   "constrain-rule",
				RuleTier: "global",
				Constraints: config.PolicyConstraints{
					Budgets:        config.BudgetConstraints{MaxRuntimeMs: 5000, MaxRetries: 2},
					RedactionLevel: "standard",
				},
				Remediations: []config.PolicyRemediation{{
					ID:    "rem-cache",
					Title: "Use cached",
				}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := legacyshim.LegacyPolicyDecisionToDecision(tc.pd)
			if err != nil {
				t.Fatalf("LegacyPolicyDecisionToDecision err = %v", err)
			}
			back, err := legacyshim.DecisionToLegacyPolicyDecision(d)
			if err != nil {
				t.Fatalf("DecisionToLegacyPolicyDecision err = %v", err)
			}
			if !reflect.DeepEqual(tc.pd, back) {
				t.Errorf("round-trip mismatch\nbefore: %+v\nafter:  %+v", tc.pd, back)
			}
		})
	}
}

func TestLegacyPolicyDecisionRejectsUnknown(t *testing.T) {
	pd := config.PolicyDecision{Decision: "bogus"}
	_, err := legacyshim.LegacyPolicyDecisionToDecision(pd)
	if !errors.Is(err, legacyshim.ErrUnknownLegacyDecision) {
		t.Fatalf("expected ErrUnknownLegacyDecision, got %v", err)
	}
}

func TestScopeProjectionRejectsWorkflowTierWithoutSelector(t *testing.T) {
	old := config.InputPolicyRule{
		ID:       "no-selector",
		Tier:     "workflow",
		Severity: "low",
		Match:    config.InputPolicyMatch{},
		Decision: "deny",
	}
	_, err := legacyshim.InputPolicyRuleToRule(old)
	if err == nil {
		t.Fatal("expected error for workflow tier without selector.workflow_id, got nil")
	}
}

func TestDecisionTypeFromLegacyStringCoversAllValues(t *testing.T) {
	cases := []struct {
		in   string
		want policy.DecisionType
	}{
		{"allow", policy.DecisionAllow},
		{"deny", policy.DecisionDeny},
		{"require_approval", policy.DecisionRequireHuman},
		{"throttle", policy.DecisionThrottle},
		{"allow_with_constraints", policy.DecisionAllowWithConstraints},
		{"quarantine", policy.DecisionQuarantine},
		{"redact", policy.DecisionRedact},
		{"  ALLOW  ", policy.DecisionAllow},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			pd := config.PolicyDecision{Decision: tc.in}
			d, err := legacyshim.LegacyPolicyDecisionToDecision(pd)
			if err != nil {
				t.Fatalf("LegacyPolicyDecisionToDecision(%q) err = %v", tc.in, err)
			}
			if d.Type != tc.want {
				t.Errorf("LegacyPolicyDecisionToDecision(%q).Type = %q, want %q", tc.in, d.Type, tc.want)
			}
		})
	}
}

func TestLegacyStringFromDecisionTypeCoversAllValues(t *testing.T) {
	cases := []struct {
		in   policy.DecisionType
		want string
	}{
		{policy.DecisionAllow, "allow"},
		{policy.DecisionDeny, "deny"},
		{policy.DecisionRequireHuman, "require_approval"},
		{policy.DecisionThrottle, "throttle"},
		{policy.DecisionAllowWithConstraints, "allow_with_constraints"},
		{policy.DecisionQuarantine, "quarantine"},
		{policy.DecisionRedact, "redact"},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			d := policy.Decision{Type: tc.in}
			pd, err := legacyshim.DecisionToLegacyPolicyDecision(d)
			if err != nil {
				t.Fatalf("DecisionToLegacyPolicyDecision(%q) err = %v", tc.in, err)
			}
			if pd.Decision != tc.want {
				t.Errorf("DecisionToLegacyPolicyDecision(%q).Decision = %q, want %q", tc.in, pd.Decision, tc.want)
			}
		})
	}
}

func TestScopeProjectionCoversAllTiers(t *testing.T) {
	cases := []struct {
		name   string
		tier   string
		sel    config.PolicySelector
		want   policy.RuleScope
		wantOK bool
	}{
		{"empty tier defaults to global", "", config.PolicySelector{}, policy.RuleScope{Kind: policy.RuleScopeGlobal}, true},
		{"global tier", "global", config.PolicySelector{}, policy.RuleScope{Kind: policy.RuleScopeGlobal}, true},
		{"workflow tier with selector", "workflow", config.PolicySelector{WorkflowID: "wf-1"}, policy.RuleScope{Kind: policy.RuleScopeWorkflow, Value: "wf-1"}, true},
		{"job tier with workflow selector", "job", config.PolicySelector{WorkflowID: "wf-1"}, policy.RuleScope{Kind: policy.RuleScopeWorkflow, Value: "wf-1"}, true},
		{"job tier without selector errors", "job", config.PolicySelector{}, policy.RuleScope{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := config.InputPolicyRule{
				ID:       "scope-test",
				Tier:     tc.tier,
				Selector: tc.sel,
				Severity: "low",
				Match:    config.InputPolicyMatch{},
				Decision: "deny",
			}
			rule, err := legacyshim.InputPolicyRuleToRule(old)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("InputPolicyRuleToRule err = %v", err)
				}
				if rule.Scope != tc.want {
					t.Errorf("rule.Scope = %+v, want %+v", rule.Scope, tc.want)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			}
		})
	}
}

func TestRuleToInputPolicyRuleRejectsMalformedMatch(t *testing.T) {
	rule := policy.Rule{
		ID:    "malformed",
		Type:  policy.RuleTypeInput,
		Match: []byte("not valid json"),
	}
	_, err := legacyshim.RuleToInputPolicyRule(rule)
	if err == nil {
		t.Fatal("expected error decoding malformed match, got nil")
	}
}

func TestRuleToOutputPolicyRuleRejectsMalformedMatch(t *testing.T) {
	rule := policy.Rule{
		ID:    "malformed",
		Type:  policy.RuleTypeOutput,
		Match: []byte("not valid json"),
	}
	_, err := legacyshim.RuleToOutputPolicyRule(rule)
	if err == nil {
		t.Fatal("expected error decoding malformed match, got nil")
	}
}

func TestRuleToPolicyRuleRejectsMalformedMatch(t *testing.T) {
	rule := policy.Rule{
		ID:    "malformed",
		Type:  policy.RuleTypeVelocity,
		Match: []byte("not valid json"),
	}
	_, err := legacyshim.RuleToPolicyRule(rule)
	if err == nil {
		t.Fatal("expected error decoding malformed match, got nil")
	}
}

func TestDecisionToLegacyPolicyDecisionRejectsUnknownType(t *testing.T) {
	d := policy.Decision{Type: policy.DecisionType("nonsense")}
	_, err := legacyshim.DecisionToLegacyPolicyDecision(d)
	if !errors.Is(err, legacyshim.ErrUnknownDecisionType) {
		t.Fatalf("expected ErrUnknownDecisionType, got %v", err)
	}
}

func TestInputPolicyRuleProducesPublishedStatus(t *testing.T) {
	old := config.InputPolicyRule{
		ID:       "status-test",
		Tier:     "global",
		Severity: "low",
		Match:    config.InputPolicyMatch{},
		Decision: "deny",
	}
	rule, err := legacyshim.InputPolicyRuleToRule(old)
	if err != nil {
		t.Fatalf("InputPolicyRuleToRule err = %v", err)
	}
	if rule.Status != policy.RuleStatusPublished {
		t.Errorf("rule.Status = %q, want %q (legacy authoring → published default)", rule.Status, policy.RuleStatusPublished)
	}
}
