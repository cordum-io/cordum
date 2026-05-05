package policybundles

import (
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

func TestMergeSafetyPolicies_BackwardCompatNoTierField(t *testing.T) {
	policy := &config.SafetyPolicy{
		DefaultDecision: "allow",
		Rules: []config.PolicyRule{
			{ID: "legacy-global-allow", Decision: "allow"},
		},
	}

	merged := MergeSafetyPolicies(nil, policy)

	if merged == nil {
		t.Fatal("expected merged policy")
	}
	if merged.Tier != config.PolicyTierGlobal {
		t.Fatalf("merged tier = %q, want global", merged.Tier)
	}
	if merged.DefaultDecision != "allow" {
		t.Fatalf("default decision = %q, want allow", merged.DefaultDecision)
	}
	if len(merged.TierDefaults) != 0 {
		t.Fatalf("unexpected scoped defaults: %+v", merged.TierDefaults)
	}
	if got := merged.Rules[0].Tier; got != config.PolicyTierGlobal {
		t.Fatalf("rule tier = %q, want global", got)
	}
}

func TestMergeSafetyPolicies_AppliesWorkflowTierSelectorAndDefault(t *testing.T) {
	base := &config.SafetyPolicy{DefaultDecision: "allow"}
	workflow := &config.SafetyPolicy{
		Tier:            config.PolicyTierWorkflow,
		Selector:        config.PolicySelector{WorkflowID: "deploy-prod"},
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{
			{ID: "workflow-deny-write", Decision: "deny"},
		},
	}

	merged := MergeSafetyPolicies(base, workflow)

	if merged.Tier != config.PolicyTierGlobal {
		t.Fatalf("aggregate tier = %q, want global", merged.Tier)
	}
	if merged.DefaultDecision != "allow" {
		t.Fatalf("global default = %q, want allow", merged.DefaultDecision)
	}
	if len(merged.TierDefaults) != 1 {
		t.Fatalf("expected one workflow default, got %+v", merged.TierDefaults)
	}
	def := merged.TierDefaults[0]
	if def.Tier != config.PolicyTierWorkflow || def.Selector.WorkflowID != "deploy-prod" || def.Decision != "deny" {
		t.Fatalf("unexpected workflow default: %+v", def)
	}
	rule := merged.Rules[0]
	if rule.Tier != config.PolicyTierWorkflow || rule.Selector.WorkflowID != "deploy-prod" {
		t.Fatalf("workflow rule did not inherit tier/selector: %+v", rule)
	}
}

func TestRulesFromPolicyContent_TagsTierInProvenance(t *testing.T) {
	rules, err := RulesFromPolicyContent("secops/deploy-prod", map[string]any{
		"version": "2026.05",
	}, `
tier: workflow
selector:
  workflow_id: deploy-prod
rules:
  - id: workflow-deny-write
    decision: deny
    match:
      topics: ["job.deploy"]
`)
	if err != nil {
		t.Fatalf("RulesFromPolicyContent: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules len = %d, want 1", len(rules))
	}
	if got := rules[0]["tier"]; got != config.PolicyTierWorkflow {
		t.Fatalf("rule tier = %#v, want workflow", got)
	}
	source, ok := rules[0]["source"].(PolicyRuleSource)
	if !ok {
		t.Fatalf("source has type %T, want PolicyRuleSource", rules[0]["source"])
	}
	if source.Tier != config.PolicyTierWorkflow {
		t.Fatalf("source tier = %q, want workflow", source.Tier)
	}
}
