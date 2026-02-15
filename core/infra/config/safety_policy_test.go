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
