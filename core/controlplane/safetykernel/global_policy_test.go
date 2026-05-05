package safetykernel

import (
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

func TestGlobalPolicy_SectionSplitByTopic(t *testing.T) {
	policy := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{
				ID:       "input-job-deploy",
				Match:    config.PolicyMatch{Topics: []string{"job.deploy"}},
				Decision: "allow",
			},
			{
				ID:       "edge-read",
				Match:    config.PolicyMatch{Topics: []string{EdgeActionTopic}},
				Decision: "deny",
			},
			{
				ID: "mcp-fs-read",
				Match: config.PolicyMatch{
					Topics: []string{"job.mcp.tools.call"},
					MCP:    config.MCPPolicy{DenyTools: []string{"fs.read"}},
				},
				Decision: "deny",
			},
		},
		OutputRules: []config.OutputPolicyRule{
			{ID: "out-secret", Match: config.OutputPolicyMatch{Keywords: []string{"OPENAI_API_KEY="}}, Decision: "quarantine"},
		},
	}

	g := FromSafetyPolicy(policy, nil, nil, "cfg:abc")

	if len(g.InputRules) != 1 || g.InputRules[0].ID != "input-job-deploy" {
		t.Fatalf("expected input bucket to contain input-job-deploy, got %+v", g.InputRules)
	}
	if len(g.EdgeActionRules) != 1 || g.EdgeActionRules[0].ID != "edge-read" {
		t.Fatalf("expected edge bucket to contain edge-read, got %+v", g.EdgeActionRules)
	}
	if len(g.MCPToolRules) != 1 || g.MCPToolRules[0].ID != "mcp-fs-read" {
		t.Fatalf("expected mcp bucket to contain mcp-fs-read, got %+v", g.MCPToolRules)
	}
	if len(g.OutputRules) != 1 || g.OutputRules[0].ID != "out-secret" {
		t.Fatalf("expected output bucket to contain out-secret, got %+v", g.OutputRules)
	}
}

func TestGlobalPolicy_SnapshotPassthrough(t *testing.T) {
	g := FromSafetyPolicy(&config.SafetyPolicy{}, nil, nil, "cfg:deadbeef")
	if g.SnapshotHash != "cfg:deadbeef" {
		t.Fatalf("SnapshotHash mismatch: got %q want %q", g.SnapshotHash, "cfg:deadbeef")
	}
	if g.SnapshotVersion != "cfg:deadbeef" {
		t.Fatalf("SnapshotVersion mismatch: got %q want %q", g.SnapshotVersion, "cfg:deadbeef")
	}
}

func TestGlobalPolicy_NilSafetyPolicy(t *testing.T) {
	g := FromSafetyPolicy(nil, nil, nil, "cfg:0")
	if g == nil {
		t.Fatal("FromSafetyPolicy(nil, ...) returned nil — must return a fail-closed empty GlobalPolicy")
	}
	if len(g.InputRules) != 0 || len(g.EdgeActionRules) != 0 || len(g.MCPToolRules) != 0 {
		t.Fatalf("nil policy must yield empty rule buckets, got %+v", g)
	}
	if len(g.OutputRules) != 0 {
		t.Fatalf("nil policy must yield empty output rules, got %+v", g.OutputRules)
	}
	if g.SnapshotHash != "cfg:0" {
		t.Fatalf("snapshot must still pass through on nil policy, got %q", g.SnapshotHash)
	}

	// Section accessors must be nil-safe on a nil receiver too.
	var nilG *GlobalPolicy
	if rules := nilG.RulesForInput(); rules != nil {
		t.Fatalf("nil receiver RulesForInput must return nil, got %+v", rules)
	}
	if rules := nilG.RulesForOutput(); rules != nil {
		t.Fatalf("nil receiver RulesForOutput must return nil, got %+v", rules)
	}
}

func TestGlobalPolicy_InvariantsAppendedToEachSection(t *testing.T) {
	policy := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "input-allow", Match: config.PolicyMatch{Topics: []string{"job.deploy"}}, Decision: "allow"},
			{ID: "edge-allow", Match: config.PolicyMatch{Topics: []string{EdgeActionTopic}}, Decision: "allow"},
			{
				ID: "mcp-allow",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{AllowTools: []string{"fs.write"}},
				},
				Decision: "allow",
			},
		},
		OutputRules: []config.OutputPolicyRule{
			{ID: "out-allow", Match: config.OutputPolicyMatch{Keywords: []string{"hello"}}, Decision: "allow"},
		},
	}

	invariants := []config.PolicyRule{
		{
			ID:       "inv-deny-secret-paths",
			Match:    config.PolicyMatch{Topics: []string{"*"}},
			Decision: "deny",
			Reason:   "deny secret-path access",
		},
	}
	outputInvariants := []config.OutputPolicyRule{
		{ID: "inv-deny-secret-output", Match: config.OutputPolicyMatch{ContentPatterns: []string{`API_KEY=`}}, Decision: "quarantine"},
	}

	g := FromSafetyPolicy(policy, invariants, outputInvariants, "cfg:1")

	// Each accessor must place Invariants FIRST so DENY-uncrossable
	// precedence holds when the matcher returns on first match.
	checkPrepend := func(t *testing.T, name string, got []config.PolicyRule, expectFirst, expectSecond string) {
		t.Helper()
		if len(got) != 2 {
			t.Fatalf("%s: expected 2 rules (invariant + section), got %d (%+v)", name, len(got), got)
		}
		if got[0].ID != expectFirst {
			t.Fatalf("%s: expected first rule to be invariant %q, got %q", name, expectFirst, got[0].ID)
		}
		if got[1].ID != expectSecond {
			t.Fatalf("%s: expected second rule to be section %q, got %q", name, expectSecond, got[1].ID)
		}
	}

	checkPrepend(t, "RulesForInput", g.RulesForInput(), "inv-deny-secret-paths", "input-allow")
	checkPrepend(t, "RulesForEdgeAction", g.RulesForEdgeAction(), "inv-deny-secret-paths", "edge-allow")
	checkPrepend(t, "RulesForMCPTool", g.RulesForMCPTool(), "inv-deny-secret-paths", "mcp-allow")

	out := g.RulesForOutput()
	if len(out) != 2 {
		t.Fatalf("RulesForOutput: expected 2 rules, got %d (%+v)", len(out), out)
	}
	if out[0].ID != "inv-deny-secret-output" {
		t.Fatalf("RulesForOutput: expected first rule to be output invariant, got %q", out[0].ID)
	}
	if out[1].ID != "out-allow" {
		t.Fatalf("RulesForOutput: expected second rule to be section, got %q", out[1].ID)
	}
}

func TestRulesForJobWorkflow_PrecedenceOrder(t *testing.T) {
	policy := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "global-allow", Decision: "allow", Match: config.PolicyMatch{Topics: []string{"job.deploy"}}},
			{
				ID:       "workflow-deny",
				Tier:     config.PolicyTierWorkflow,
				Selector: config.PolicySelector{WorkflowID: "deploy-prod"},
				Decision: "deny",
				Match:    config.PolicyMatch{Topics: []string{"job.deploy"}},
			},
			{
				ID:       "job-allow",
				Tier:     config.PolicyTierJob,
				Selector: config.PolicySelector{JobID: "job-123"},
				Decision: "allow",
				Match:    config.PolicyMatch{Topics: []string{"job.deploy"}},
			},
			{
				ID:       "workflow-other",
				Tier:     config.PolicyTierWorkflow,
				Selector: config.PolicySelector{WorkflowID: "deploy-dev"},
				Decision: "allow",
				Match:    config.PolicyMatch{Topics: []string{"job.deploy"}},
			},
		},
	}
	invariants := []config.PolicyRule{{ID: "inv-deny", Decision: "deny"}}

	g := FromSafetyPolicy(policy, invariants, nil, "cfg:tiers")
	got := testRuleIDs(g.RulesForJobWorkflow("deploy-prod", "job-123"))
	want := []string{"inv-deny", "job-allow", "workflow-deny", "global-allow"}

	if !equalStrings(got, want) {
		t.Fatalf("rule order = %v, want %v", got, want)
	}
}

func TestGlobalPolicy_InvariantDenyUncrossable(t *testing.T) {
	policy := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{
				ID:       "job-allow-secret",
				Tier:     config.PolicyTierJob,
				Selector: config.PolicySelector{JobID: "job-123"},
				Decision: "allow",
			},
		},
	}
	invariants := []config.PolicyRule{{ID: "inv-deny-secret", Decision: "deny", Tier: config.PolicyTierJob}}

	g := FromSafetyPolicy(policy, invariants, nil, "cfg:tiers")
	rules := g.RulesForJobWorkflow("", "job-123")

	if len(rules) != 2 {
		t.Fatalf("rules len = %d, want 2 (%+v)", len(rules), rules)
	}
	if rules[0].ID != "inv-deny-secret" || rules[0].Tier != config.PolicyTierGlobal {
		t.Fatalf("invariant must be first and forced global tier, got %+v", rules[0])
	}
	if rules[1].ID != "job-allow-secret" {
		t.Fatalf("job override should remain behind invariant, got %+v", rules[1])
	}
}

func TestGlobalPolicy_BackwardCompatNoTierField(t *testing.T) {
	policy := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "legacy-global", Decision: "allow", Match: config.PolicyMatch{Topics: []string{"job.*"}}},
		},
	}

	g := FromSafetyPolicy(policy, nil, nil, "cfg:legacy")

	if len(g.InputRules) != 1 {
		t.Fatalf("InputRules len = %d, want 1", len(g.InputRules))
	}
	if g.InputRules[0].Tier != config.PolicyTierGlobal {
		t.Fatalf("legacy rule tier = %q, want global", g.InputRules[0].Tier)
	}
	got := testRuleIDs(g.RulesForJobWorkflow("missing-wf", "missing-job"))
	want := []string{"legacy-global"}
	if !equalStrings(got, want) {
		t.Fatalf("legacy rules = %v, want %v", got, want)
	}
}

func TestGlobalPolicy_NoMatchUsesMostRestrictiveDefault(t *testing.T) {
	policy := &config.SafetyPolicy{
		DefaultDecision: "allow",
		TierDefaults: []config.PolicyTierDefault{
			{
				Tier:     config.PolicyTierWorkflow,
				Selector: config.PolicySelector{WorkflowID: "deploy-prod"},
				Decision: "deny",
			},
			{
				Tier:     config.PolicyTierJob,
				Selector: config.PolicySelector{JobID: "job-allow"},
				Decision: "allow",
			},
		},
		Rules: []config.PolicyRule{
			{
				ID:       "job-no-default",
				Tier:     config.PolicyTierJob,
				Selector: config.PolicySelector{JobID: "job-no-default"},
				Decision: "allow",
			},
		},
	}

	g := FromSafetyPolicy(policy, nil, nil, "cfg:defaults")

	if got := g.DefaultDecisionForJobWorkflow("deploy-prod", "job-allow"); got != "allow" {
		t.Fatalf("job default = %q, want allow", got)
	}
	if got := g.DefaultDecisionForJobWorkflow("deploy-prod", "job-no-default"); got != "deny" {
		t.Fatalf("job tier without explicit default = %q, want deny", got)
	}
	if got := g.DefaultDecisionForJobWorkflow("deploy-prod", ""); got != "deny" {
		t.Fatalf("workflow default = %q, want deny", got)
	}
	if got := g.DefaultDecisionForJobWorkflow("deploy-dev", ""); got != "allow" {
		t.Fatalf("global default = %q, want allow", got)
	}
}

func testRuleIDs(rules []config.PolicyRule) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.ID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if a[idx] != b[idx] {
			return false
		}
	}
	return true
}
