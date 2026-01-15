package config

import "testing"

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
		"":                       "allow",
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
