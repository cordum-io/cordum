package config

import (
	"testing"
)

func TestParseSafetyPolicyEmpty(t *testing.T) {
	policy, err := ParseSafetyPolicy(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy != nil {
		t.Fatalf("expected nil policy for empty input")
	}
}

func TestEvaluateRuleMatch(t *testing.T) {
	policy := &SafetyPolicy{
		Rules: []PolicyRule{
			{
				ID:       "rule1",
				Decision: "deny",
				Reason:   "blocked",
				Match: PolicyMatch{
					Tenants:      []string{"t1"},
					Topics:       []string{"job.sre.*"},
					Capabilities: []string{"cap"},
					RiskTags:     []string{"write"},
					Requires:     []string{"git"},
					Labels:       map[string]string{"env": "prod"},
				},
			},
		},
	}
	input := PolicyInput{
		Tenant: "t1",
		Topic:  "job.sre.collect",
		Labels: map[string]string{"env": "prod"},
		Meta: PolicyMeta{
			Capability: "cap",
			RiskTags:   []string{"write"},
			Requires:   []string{"git", "net"},
			PackID:     "pack",
		},
	}
	dec := policy.Evaluate(input)
	if dec.Decision != "deny" || dec.RuleID != "rule1" {
		t.Fatalf("unexpected decision: %#v", dec)
	}
}

func TestEvaluateLegacyRules(t *testing.T) {
	policy := &SafetyPolicy{Tenants: map[string]TenantPolicy{
		"t1": {AllowTopics: []string{"job.allowed"}, DenyTopics: []string{"job.blocked"}},
	}}
	dec := policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.blocked"})
	if dec.Decision != "deny" {
		t.Fatalf("expected deny decision")
	}
	dec = policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.allowed"})
	if dec.Decision != "allow" {
		t.Fatalf("expected allow decision")
	}
}

func TestEvaluateRemediations(t *testing.T) {
	policy := &SafetyPolicy{
		Rules: []PolicyRule{
			{
				ID:       "rule-remediate",
				Decision: "deny",
				Match: PolicyMatch{
					Tenants: []string{"t1"},
					Topics:  []string{"job.db.delete"},
				},
				Remediations: []PolicyRemediation{
					{
						ID:               "archive",
						Title:            "Archive instead of delete",
						Summary:          "Use archive flow for safer retention",
						ReplacementTopic: "job.db.archive",
					},
				},
			},
		},
	}
	dec := policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.db.delete"})
	if len(dec.Remediations) != 1 || dec.Remediations[0].ReplacementTopic != "job.db.archive" {
		t.Fatalf("expected remediation in decision: %#v", dec.Remediations)
	}
}

func TestNormalizeDecision(t *testing.T) {
	cases := map[string]string{
		"permit":                 "allow",
		"block":                  "deny",
		"require-approval":       "require_approval",
		"allow_with_constraints": "allow_with_constraints",
		"throttle":               "throttle",
		"":                       "deny",
	}
	for input, expect := range cases {
		if got := normalizeDecision(input); got != expect {
			t.Fatalf("normalize %q expected %q got %q", input, expect, got)
		}
	}
}

func TestMatchRuleSecretsAndMCP(t *testing.T) {
	flag := true
	match := PolicyMatch{
		SecretsPresent: &flag,
		MCP: MCPPolicy{
			AllowServers: []string{"srv"},
		},
	}
	input := PolicyInput{
		SecretsPresent: true,
		MCP:            MCPRequest{Server: "srv"},
	}
	if !matchRule(match, input) {
		t.Fatalf("expected rule to match")
	}
	input.SecretsPresent = false
	if matchRule(match, input) {
		t.Fatalf("expected rule to fail on secrets")
	}
}

func TestMCPAllowed(t *testing.T) {
	policy := MCPPolicy{
		AllowServers: []string{"srv"},
		DenyTools:    []string{"bad"},
	}
	ok, reason := MCPAllowed(policy, MCPRequest{Server: "srv", Tool: "bad"})
	if ok || reason == "" {
		t.Fatalf("expected denied tool")
	}
	ok, reason = MCPAllowed(policy, MCPRequest{Server: "srv", Tool: "good"})
	if !ok || reason != "" {
		t.Fatalf("expected allowed tool")
	}
}

func TestParseSafetyPolicyInvalidDecision(t *testing.T) {
	_, err := ParseSafetyPolicy([]byte("rules:\n  - id: rule1\n    decision: maybe\n"))
	if err == nil {
		t.Fatalf("expected schema validation error")
	}
}

func TestParseSafetyPolicyOutputRules(t *testing.T) {
	policyYAML := []byte(`
version: "1"
output_policy:
  enabled: true
  fail_mode: open
output_rules:
  - id: out-1
    enabled: true
    severity: high
    description: test output rule
    decision: redact
    reason: remove secret
    match:
      tenants: ["default"]
      topics: ["job.demo.*"]
      capabilities: ["code.write"]
      risk_tags: ["secrets"]
      scanners: ["secret"]
      content_patterns: ["AKIA[0-9A-Z]{16}"]
      detectors: ["secret_leak"]
      output_size_gt: 2048
      has_error: false
      max_output_bytes: 1024
`)
	policy, err := ParseSafetyPolicy(policyYAML)
	if err != nil {
		t.Fatalf("expected output rules to parse: %v", err)
	}
	if policy == nil || len(policy.OutputRules) != 1 {
		t.Fatalf("expected one output rule, got %#v", policy)
	}
	if !policy.OutputPolicy.Enabled || policy.OutputPolicy.FailMode != "open" {
		t.Fatalf("unexpected output policy config: %#v", policy.OutputPolicy)
	}
	if policy.OutputRules[0].Decision != "redact" || policy.OutputRules[0].Match.MaxOutputBytes != 1024 {
		t.Fatalf("unexpected output rule parse: %#v", policy.OutputRules[0])
	}
	if policy.OutputRules[0].Severity != "high" || policy.OutputRules[0].Desc != "test output rule" {
		t.Fatalf("unexpected output rule metadata parse: %#v", policy.OutputRules[0])
	}
	if policy.OutputRules[0].Match.OutputSizeGt != 2048 {
		t.Fatalf("unexpected output_size_gt parse: %#v", policy.OutputRules[0].Match)
	}
	if policy.OutputRules[0].Match.HasError == nil || *policy.OutputRules[0].Match.HasError {
		t.Fatalf("unexpected has_error parse: %#v", policy.OutputRules[0].Match)
	}
	if len(policy.OutputRules[0].Match.Scanners) != 1 || policy.OutputRules[0].Match.Scanners[0] != "secret" {
		t.Fatalf("unexpected scanners parse: %#v", policy.OutputRules[0].Match)
	}
}

func TestParseSafetyPolicyOutputRuleInvalidDecision(t *testing.T) {
	_, err := ParseSafetyPolicy([]byte(`
output_rules:
  - id: out-1
    decision: maybe
    match:
      detectors: ["secret_leak"]
`))
	if err == nil {
		t.Fatalf("expected schema validation error for invalid output rule decision")
	}
}

func TestEvaluateDefaultDecisionDeny(t *testing.T) {
	policy := &SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []PolicyRule{
			{ID: "rule1", Decision: "allow", Match: PolicyMatch{Topics: []string{"job.allowed"}}},
		},
	}
	// No matching rule → should use default deny
	dec := policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.unmatched"})
	if dec.Decision != "deny" {
		t.Fatalf("expected deny default, got %q", dec.Decision)
	}
	if dec.Reason != "no matching rule — default policy: deny" {
		t.Fatalf("unexpected reason: %q", dec.Reason)
	}
}

func TestEvaluateDefaultDecisionAllow(t *testing.T) {
	policy := &SafetyPolicy{
		DefaultDecision: "allow",
		Rules: []PolicyRule{
			{ID: "rule1", Decision: "deny", Match: PolicyMatch{Topics: []string{"job.blocked"}}},
		},
	}
	// No matching rule → should use default allow
	dec := policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.unmatched"})
	if dec.Decision != "allow" {
		t.Fatalf("expected allow default, got %q", dec.Decision)
	}
}

func TestEvaluateDefaultDecisionEmpty(t *testing.T) {
	policy := &SafetyPolicy{
		// DefaultDecision empty → should be deny (fail-closed)
		Rules: []PolicyRule{
			{ID: "rule1", Decision: "allow", Match: PolicyMatch{Topics: []string{"job.allowed"}}},
		},
	}
	dec := policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.unmatched"})
	if dec.Decision != "deny" {
		t.Fatalf("expected deny for empty default_decision, got %q", dec.Decision)
	}
}

func TestEvaluateDefaultDecisionIrrelevantOnMatch(t *testing.T) {
	policy := &SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []PolicyRule{
			{ID: "rule1", Decision: "allow", Match: PolicyMatch{Topics: []string{"job.ok"}}},
		},
	}
	// Rule matches → default is irrelevant
	dec := policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.ok"})
	if dec.Decision != "allow" {
		t.Fatalf("expected allow from matching rule, got %q", dec.Decision)
	}
}

func TestParseSafetyPolicyDefaultDecision(t *testing.T) {
	policy, err := ParseSafetyPolicy([]byte(`
default_decision: deny
rules:
  - id: rule1
    decision: allow
    match:
      topics: ["job.test"]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy.DefaultDecision != "deny" {
		t.Fatalf("expected default_decision deny, got %q", policy.DefaultDecision)
	}
}

func TestParseSafetyPolicyTierSelectorFields(t *testing.T) {
	policy, err := ParseSafetyPolicy([]byte(`
tier: workflow
selector:
  workflow_id: deploy-prod
default_decision: deny
rules:
  - id: workflow-deny-write
    decision: deny
    match:
      topics: ["job.deploy"]
`))
	if err != nil {
		t.Fatalf("expected workflow tier policy to parse: %v", err)
	}
	if policy.Tier != PolicyTierWorkflow {
		t.Fatalf("tier = %q, want workflow", policy.Tier)
	}
	if policy.Selector.WorkflowID != "deploy-prod" {
		t.Fatalf("workflow selector = %q, want deploy-prod", policy.Selector.WorkflowID)
	}
}

func TestParseSafetyPolicyScopedTierRequiresSelector(t *testing.T) {
	_, err := ParseSafetyPolicy([]byte(`
tier: job
default_decision: deny
rules:
  - id: job-deny-write
    decision: deny
    match:
      topics: ["job.deploy"]
`))
	if err == nil {
		t.Fatalf("expected job tier without job_id/session_id selector to fail")
	}
}

func TestParseSafetyPolicyInputRuleTierSelectorFields(t *testing.T) {
	policy, err := ParseSafetyPolicy([]byte(`
input_rules:
  - id: job-deny-secret-input
    tier: job
    selector:
      session_id: edgesess-1
    severity: high
    decision: deny
    reason: scoped input secret
    match:
      topics: ["job.default"]
      keywords: ["secret"]
`))
	if err != nil {
		t.Fatalf("expected scoped input rule to parse: %v", err)
	}
	if len(policy.InputRules) != 1 {
		t.Fatalf("input rules len = %d, want 1", len(policy.InputRules))
	}
	rule := policy.InputRules[0]
	if rule.Tier != PolicyTierJob {
		t.Fatalf("input rule tier = %q, want job", rule.Tier)
	}
	if rule.Selector.SessionID != "edgesess-1" {
		t.Fatalf("input rule session selector = %q, want edgesess-1", rule.Selector.SessionID)
	}
}

func TestParseSafetyPolicyScopedInputRuleRequiresSelector(t *testing.T) {
	_, err := ParseSafetyPolicy([]byte(`
input_rules:
  - id: workflow-deny-secret-input
    tier: workflow
    severity: high
    decision: deny
    match:
      topics: ["job.default"]
      keywords: ["secret"]
`))
	if err == nil {
		t.Fatalf("expected workflow-tier input_rule without workflow_id selector to fail")
	}
}

func TestParseSafetyPolicyAgentScopeMatchFields(t *testing.T) {
	policy, err := ParseSafetyPolicy([]byte(`
rules:
  - id: agent-scoped
    decision: allow
    match:
      topics: ["job.agent"]
      agent_risk_tiers: ["critical"]
      agent_data_classifications: ["internal", "secrets"]
`))
	if err != nil {
		t.Fatalf("expected agent scope match fields to parse: %v", err)
	}
	if policy == nil || len(policy.Rules) != 1 {
		t.Fatalf("expected one rule, got %#v", policy)
	}
	match := policy.Rules[0].Match
	if len(match.AgentRiskTiers) != 1 || match.AgentRiskTiers[0] != "critical" {
		t.Fatalf("unexpected agent risk tiers: %#v", match.AgentRiskTiers)
	}
	if len(match.AgentDataClassifications) != 2 ||
		match.AgentDataClassifications[0] != "internal" ||
		match.AgentDataClassifications[1] != "secrets" {
		t.Fatalf("unexpected agent data classifications: %#v", match.AgentDataClassifications)
	}
}

func TestParseSafetyPolicyInvalidDefaultDecision(t *testing.T) {
	_, err := ParseSafetyPolicy([]byte(`default_decision: maybe`))
	if err == nil {
		t.Fatalf("expected schema validation error for invalid default_decision")
	}
}

func TestParseSafetyPolicyInvalidOutputFailMode(t *testing.T) {
	_, err := ParseSafetyPolicy([]byte(`
output_policy:
  enabled: true
  fail_mode: maybe
`))
	if err == nil {
		t.Fatalf("expected schema validation error for invalid output fail mode")
	}
}

func TestNormalizeDecisionUnknown(t *testing.T) {
	// A typo like "denyy" must default to deny (fail-closed), not allow.
	got := normalizeDecision("denyy")
	if got != "deny" {
		t.Fatalf("expected deny for typo 'denyy', got %q", got)
	}
	got = normalizeDecision("alllow")
	if got != "deny" {
		t.Fatalf("expected deny for typo 'alllow', got %q", got)
	}
	got = normalizeDecision("maybe")
	if got != "deny" {
		t.Fatalf("expected deny for invalid 'maybe', got %q", got)
	}
}

func TestNormalizeDecisionEmpty(t *testing.T) {
	got := normalizeDecision("")
	if got != "deny" {
		t.Fatalf("expected deny for empty string, got %q", got)
	}
	got = normalizeDecision("   ")
	if got != "deny" {
		t.Fatalf("expected deny for whitespace-only string, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Security regression: default decision fail-closed behavior
// ---------------------------------------------------------------------------

func TestEvaluateNilRulesAndTenantsDefaultsDeny(t *testing.T) {
	// Policy with no rules and no tenants must default to deny (fail-closed),
	// not silently allow everything.
	policy := &SafetyPolicy{}
	dec := policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.anything"})
	if dec.Decision != "deny" {
		t.Fatalf("FAIL-OPEN: empty policy should deny, got %q", dec.Decision)
	}
}

func TestEvaluateDefaultDecisionCaseInsensitive(t *testing.T) {
	// DefaultDecision should be case-insensitive.
	policy := &SafetyPolicy{DefaultDecision: "ALLOW"}
	dec := policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.test"})
	if dec.Decision != "allow" {
		t.Fatalf("expected allow for 'ALLOW', got %q", dec.Decision)
	}

	policy2 := &SafetyPolicy{DefaultDecision: "DENY"}
	dec2 := policy2.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.test"})
	if dec2.Decision != "deny" {
		t.Fatalf("expected deny for 'DENY', got %q", dec2.Decision)
	}
}

func TestEvaluateDefaultDecisionInvalidValueDenies(t *testing.T) {
	// An invalid default_decision value must result in deny (fail-closed).
	// This covers typos like "alow" or "permitt".
	policy := &SafetyPolicy{DefaultDecision: "alow"}
	dec := policy.Evaluate(PolicyInput{Tenant: "t1", Topic: "job.test"})
	if dec.Decision != "deny" {
		t.Fatalf("FAIL-OPEN: invalid default_decision 'alow' should deny, got %q", dec.Decision)
	}
}

func TestMCPAllowedEmptyPolicyPassesThrough(t *testing.T) {
	// An empty MCP policy should allow all MCP requests (no restrictions configured).
	policy := MCPPolicy{}
	ok, reason := MCPAllowed(policy, MCPRequest{Server: "any", Tool: "any"})
	if !ok {
		t.Fatalf("expected empty MCP policy to allow, got denied: %s", reason)
	}
}

func TestMCPAllowedDenyTakesPrecedence(t *testing.T) {
	// When a server is in both allow and deny lists, deny must take precedence.
	policy := MCPPolicy{
		AllowServers: []string{"srv"},
		DenyServers:  []string{"srv"},
	}
	ok, _ := MCPAllowed(policy, MCPRequest{Server: "srv"})
	if ok {
		t.Fatalf("expected deny to take precedence over allow for same server")
	}
}

func TestMatchRuleEmptyMatchMatchesEverything(t *testing.T) {
	// A rule with empty match criteria matches all inputs — this is by design
	// for "catch-all" rules, but we want to document the behavior.
	match := PolicyMatch{}
	input := PolicyInput{Tenant: "t1", Topic: "job.test"}
	if !matchRule(match, input) {
		t.Fatalf("expected empty match to match any input")
	}
}

func TestMatchRuleLabels_InternalLabel(t *testing.T) {
	// Rule requires _internal=true label.
	match := PolicyMatch{
		Topics: []string{"job.default"},
		Labels: map[string]string{"_internal": "true"},
	}

	// With label → match.
	withLabel := PolicyInput{
		Topic:  "job.default",
		Labels: map[string]string{"_internal": "true"},
	}
	if !matchRule(match, withLabel) {
		t.Fatal("expected match when _internal label present")
	}

	// Without label → no match.
	withoutLabel := PolicyInput{
		Topic:  "job.default",
		Labels: map[string]string{},
	}
	if matchRule(match, withoutLabel) {
		t.Fatal("expected no match when _internal label missing")
	}

	// Wrong value → no match.
	wrongValue := PolicyInput{
		Topic:  "job.default",
		Labels: map[string]string{"_internal": "false"},
	}
	if matchRule(match, wrongValue) {
		t.Fatal("expected no match when _internal label has wrong value")
	}

	// Nil labels → no match.
	nilLabels := PolicyInput{Topic: "job.default"}
	if matchRule(match, nilLabels) {
		t.Fatal("expected no match when labels are nil")
	}
}

func TestEvaluate_DefaultTopicRestriction(t *testing.T) {
	// Red-team scenario #2: job.default with internal label → allow,
	// without label → require_approval.
	policy := &SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []PolicyRule{
			{
				ID:       "default-topic-internal-allow",
				Decision: "allow",
				Reason:   "Internal probe on default topic.",
				Match: PolicyMatch{
					Topics: []string{"job.default"},
					Labels: map[string]string{"_internal": "true"},
				},
			},
			{
				ID:       "default-topic-external-review",
				Decision: "require_approval",
				Reason:   "External use of job.default requires approval.",
				Match: PolicyMatch{
					Topics: []string{"job.default"},
				},
			},
		},
	}

	// Internal probe with label → allow.
	internal := policy.Evaluate(PolicyInput{
		Tenant: "default",
		Topic:  "job.default",
		Labels: map[string]string{"_internal": "true"},
	})
	if internal.Decision != "allow" {
		t.Fatalf("internal probe: expected allow, got %q", internal.Decision)
	}
	if internal.RuleID != "default-topic-internal-allow" {
		t.Fatalf("expected rule default-topic-internal-allow, got %q", internal.RuleID)
	}

	// External caller without label → require_approval.
	external := policy.Evaluate(PolicyInput{
		Tenant: "default",
		Topic:  "job.default",
	})
	if external.Decision != "require_approval" {
		t.Fatalf("RED-TEAM BYPASS: external job.default without label: expected require_approval, got %q",
			external.Decision)
	}
	if external.RuleID != "default-topic-external-review" {
		t.Fatalf("expected rule default-topic-external-review, got %q", external.RuleID)
	}

	// External caller with wrong label → require_approval (NOT allow).
	wrongLabel := policy.Evaluate(PolicyInput{
		Tenant: "default",
		Topic:  "job.default",
		Labels: map[string]string{"_internal": "false"},
	})
	if wrongLabel.Decision != "require_approval" {
		t.Fatalf("wrong label value: expected require_approval, got %q", wrongLabel.Decision)
	}
}

func TestEvaluate_SourceRestriction(t *testing.T) {
	// Red-team scenarios #17/#18: blanket-allow topics must be restricted
	// to workflow/pack sources. Direct API callers get require_approval.
	policy := &SafetyPolicy{
		DefaultDecision: "deny",
		Rules: []PolicyRule{
			{
				ID:       "b2b-internal-allow",
				Decision: "allow",
				Match: PolicyMatch{
					Topics: []string{"job.b2b.orchestrate"},
					Labels: map[string]string{"_source": "workflow"},
				},
			},
			{
				ID:       "b2b-pack-allow",
				Decision: "allow",
				Match: PolicyMatch{
					Topics: []string{"job.b2b.orchestrate"},
					Labels: map[string]string{"_source": "pack"},
				},
			},
			{
				ID:       "b2b-api-review",
				Decision: "require_approval",
				Match:    PolicyMatch{Topics: []string{"job.b2b.orchestrate"}},
			},
		},
	}

	// Workflow source → allow.
	wfResult := policy.Evaluate(PolicyInput{
		Topic:  "job.b2b.orchestrate",
		Labels: map[string]string{"_source": "workflow"},
	})
	if wfResult.Decision != "allow" {
		t.Fatalf("workflow source: expected allow, got %q", wfResult.Decision)
	}

	// Pack source → allow.
	packResult := policy.Evaluate(PolicyInput{
		Topic:  "job.b2b.orchestrate",
		Labels: map[string]string{"_source": "pack"},
	})
	if packResult.Decision != "allow" {
		t.Fatalf("pack source: expected allow, got %q", packResult.Decision)
	}

	// Direct API source → require_approval.
	apiResult := policy.Evaluate(PolicyInput{
		Topic:  "job.b2b.orchestrate",
		Labels: map[string]string{"_source": "api"},
	})
	if apiResult.Decision != "require_approval" {
		t.Fatalf("RED-TEAM BYPASS: API source on b2b.orchestrate: expected require_approval, got %q",
			apiResult.Decision)
	}
	if apiResult.RuleID != "b2b-api-review" {
		t.Fatalf("expected b2b-api-review rule, got %q", apiResult.RuleID)
	}

	// No source label → require_approval (catch-all).
	noSource := policy.Evaluate(PolicyInput{
		Topic: "job.b2b.orchestrate",
	})
	if noSource.Decision != "require_approval" {
		t.Fatalf("no source: expected require_approval, got %q", noSource.Decision)
	}
}
