package config

import (
	"testing"
	"time"
)

// policyRuleIDs returns rule IDs in slice order for order-sensitive assertions.
func policyRuleIDs(rules []PolicyRule) []string {
	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.ID)
	}
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMergePolicies_DedupRulesLastSeenWinsInOrder pins the shared merge's rule
// dedup: a duplicate rule ID is REPLACED in place by the later (extra) rule
// (last-seen wins) while preserving first-seen position; a new ID appends. This
// is the cross-bundle recency contract both the kernel loader and the gateway
// bundle compiler rely on (the caller controls precedence via merge order).
func TestMergePolicies_DedupRulesLastSeenWinsInOrder(t *testing.T) {
	base := &SafetyPolicy{Rules: []PolicyRule{
		{ID: "dup", Decision: "deny"},
		{ID: "keep", Decision: "allow"},
	}}
	extra := &SafetyPolicy{Rules: []PolicyRule{
		{ID: "dup", Decision: "allow"},
		{ID: "new", Decision: "deny"},
	}}
	merged := MergePolicies(base, extra)
	if got, want := policyRuleIDs(merged.Rules), []string{"dup", "keep", "new"}; !equalStrings(got, want) {
		t.Fatalf("rule order: got %v, want %v", got, want)
	}
	if merged.Rules[0].Decision != "allow" {
		t.Fatalf("dup must be replaced last-seen-wins: got decision %q, want allow", merged.Rules[0].Decision)
	}
	if merged.Rules[2].Decision != "deny" {
		t.Fatalf("new rule decision: got %q, want deny", merged.Rules[2].Decision)
	}
}

// TestMergePolicies_DedupInputAndOutputRules mirrors the gateway oracle
// (TestMergeSafetyPolicies_DedupesInputAndOutputRules): InputRules and
// OutputRules dedup last-seen-wins and are never dropped.
func TestMergePolicies_DedupInputAndOutputRules(t *testing.T) {
	base := &SafetyPolicy{
		InputRules:  []InputPolicyRule{{ID: "in1", Decision: "deny"}},
		OutputRules: []OutputPolicyRule{{ID: "out1", Decision: "quarantine"}},
	}
	extra := &SafetyPolicy{
		InputRules:  []InputPolicyRule{{ID: "in1", Decision: "require_approval"}},
		OutputRules: []OutputPolicyRule{{ID: "out1", Decision: "redact"}},
	}
	merged := MergePolicies(base, extra)
	if len(merged.InputRules) != 1 || merged.InputRules[0].Decision != "require_approval" {
		t.Fatalf("input rule dedup last-seen-wins failed: %+v", merged.InputRules)
	}
	if len(merged.OutputRules) != 1 || merged.OutputRules[0].Decision != "redact" {
		t.Fatalf("output rule dedup last-seen-wins failed: %+v", merged.OutputRules)
	}
}

// TestMergePolicies_OutputPolicyAnyEnable pins the canonical cross-fragment
// OutputPolicy semantics (task-198acafd): a guard enabled by ANY fragment stays
// enabled (OR-merge) and FailMode fills first-non-empty with base precedence.
// This is the SINGLE behavior for both callers (the kernel loader + the gateway
// bundle compiler), so they produce IDENTICAL OutputPolicy — replacing the old
// per-caller PolicyMergeOptions divergence.
func TestMergePolicies_OutputPolicyAnyEnable(t *testing.T) {
	// base disabled + empty FailMode; a later fragment enables with FailMode.
	base := &SafetyPolicy{OutputPolicy: OutputPolicyConfig{Enabled: false, FailMode: ""}}
	extra := &SafetyPolicy{OutputPolicy: OutputPolicyConfig{Enabled: true, FailMode: "closed"}}
	merged := MergePolicies(base, extra)
	if !merged.OutputPolicy.Enabled || merged.OutputPolicy.FailMode != "closed" {
		t.Fatalf("any fragment enabling output scanning must OR-enable + take first non-empty FailMode: %+v", merged.OutputPolicy)
	}
	// base precedence: a non-empty base FailMode wins; enabled stays true.
	base2 := &SafetyPolicy{OutputPolicy: OutputPolicyConfig{Enabled: true, FailMode: "open"}}
	extra2 := &SafetyPolicy{OutputPolicy: OutputPolicyConfig{Enabled: false, FailMode: "closed"}}
	merged2 := MergePolicies(base2, extra2)
	if !merged2.OutputPolicy.Enabled || merged2.OutputPolicy.FailMode != "open" {
		t.Fatalf("base OutputPolicy must stay enabled with base-precedence FailMode: %+v", merged2.OutputPolicy)
	}
}

// TestMergePolicies_PreservesRequireHuman proves the shared clone preserves
// SafetyPolicy.RequireHuman (read by the kernel at policy-load time). The base
// fragment's threshold survives the merge, and the nil-base path preserves the
// surviving fragment's threshold.
func TestMergePolicies_PreservesRequireHuman(t *testing.T) {
	want := RequireHumanThreshold{MinSeverityForDeny: "high", MinConfidenceForDeny: 0.8, DowngradeWhenPromptOnly: true}
	merged := MergePolicies(&SafetyPolicy{RequireHuman: want}, &SafetyPolicy{})
	if merged.RequireHuman != want {
		t.Fatalf("base RequireHuman must survive merge: got %+v, want %+v", merged.RequireHuman, want)
	}
	only := MergePolicies(nil, &SafetyPolicy{RequireHuman: want})
	if only.RequireHuman != want {
		t.Fatalf("nil-base path must preserve RequireHuman: got %+v, want %+v", only.RequireHuman, want)
	}
}

// TestMergePolicies_RequireHumanFilledFromLaterFragment proves a base fragment
// WITHOUT a RequireHuman threshold inherits the threshold introduced by a later
// (extra) fragment — first-non-empty fill, base precedence — instead of dropping
// it. Without the fill the kernel keeps routing truly-ambiguous matches to hard
// DENY rather than REQUIRE_HUMAN. This FAILS on the old behavior (zero-value
// RequireHuman survived) and PASSES on the fix.
func TestMergePolicies_RequireHumanFilledFromLaterFragment(t *testing.T) {
	base := &SafetyPolicy{} // no RequireHuman authored
	later := RequireHumanThreshold{MinSeverityForDeny: "high", MinConfidenceForDeny: 0.8, DowngradeWhenPromptOnly: true}
	extra := &SafetyPolicy{RequireHuman: later}
	merged := MergePolicies(base, extra)
	if merged.RequireHuman != later {
		t.Fatalf("later fragment's RequireHuman must fill an unset base: got %+v, want %+v", merged.RequireHuman, later)
	}
	// Base precedence: a non-empty base RequireHuman is NOT overwritten by extra.
	baseThreshold := RequireHumanThreshold{MinSeverityForDeny: "critical"}
	merged2 := MergePolicies(&SafetyPolicy{RequireHuman: baseThreshold}, extra)
	if merged2.RequireHuman != baseThreshold {
		t.Fatalf("non-empty base RequireHuman must win (base precedence): got %+v, want %+v", merged2.RequireHuman, baseThreshold)
	}
}

// TestMergePolicies_SingleFragmentNormalizesToGlobal proves the nil fast-paths
// normalize a single workflow/job-scoped fragment to tier=global with an empty
// selector — matching the multi-fragment path. Without the fix the cloned
// fragment stayed in its SCOPED shape, so a single scoped bundle merged through
// either nil branch leaked a non-global tier/selector into the effective policy.
// This FAILS on the old behavior and PASSES on the fix.
func TestMergePolicies_SingleFragmentNormalizesToGlobal(t *testing.T) {
	scoped := &SafetyPolicy{Tier: PolicyTierWorkflow, Selector: PolicySelector{WorkflowID: "wf-prod"}}
	// nil base path (extra is the lone fragment).
	fromExtra := MergePolicies(nil, scoped)
	if fromExtra.Tier != PolicyTierGlobal {
		t.Fatalf("nil-base path must normalize Tier to global: got %q", fromExtra.Tier)
	}
	if fromExtra.Selector != (PolicySelector{}) {
		t.Fatalf("nil-base path must clear Selector: got %+v", fromExtra.Selector)
	}
	// nil extra path (base is the lone fragment).
	fromBase := MergePolicies(scoped, nil)
	if fromBase.Tier != PolicyTierGlobal {
		t.Fatalf("nil-extra path must normalize Tier to global: got %q", fromBase.Tier)
	}
	if fromBase.Selector != (PolicySelector{}) {
		t.Fatalf("nil-extra path must clear Selector: got %+v", fromBase.Selector)
	}
}

// TestMergePolicies_FirstNonEmptyScalars pins base-precedence first-non-empty
// fill of Version/DefaultTenant/DefaultDecision.
func TestMergePolicies_FirstNonEmptyScalars(t *testing.T) {
	extra := &SafetyPolicy{Version: "2", DefaultTenant: "acme", DefaultDecision: "deny"}
	merged := MergePolicies(&SafetyPolicy{}, extra)
	if merged.Version != "2" || merged.DefaultTenant != "acme" || merged.DefaultDecision != "deny" {
		t.Fatalf("empty base must take extra's scalars: %+v", merged)
	}
	base := &SafetyPolicy{Version: "1", DefaultTenant: "base-tenant", DefaultDecision: "allow"}
	merged2 := MergePolicies(base, extra)
	if merged2.Version != "1" || merged2.DefaultTenant != "base-tenant" || merged2.DefaultDecision != "allow" {
		t.Fatalf("non-empty base scalars must win: %+v", merged2)
	}
}

// TestMergePolicies_TierMetadataInheritance proves a cloned fragment inherits
// its tier/selector before merge: a workflow-tier extra fragment's input rule
// keeps the workflow tier + selector in the merged result.
func TestMergePolicies_TierMetadataInheritance(t *testing.T) {
	base := &SafetyPolicy{InputRules: []InputPolicyRule{{ID: "ir1", Decision: "deny"}}}
	extra := &SafetyPolicy{
		Tier:       PolicyTierWorkflow,
		Selector:   PolicySelector{WorkflowID: "wf-prod"},
		InputRules: []InputPolicyRule{{ID: "ir2", Decision: "require_approval"}},
	}
	merged := MergePolicies(base, extra)
	var ir2 *InputPolicyRule
	for i := range merged.InputRules {
		if merged.InputRules[i].ID == "ir2" {
			ir2 = &merged.InputRules[i]
		}
	}
	if ir2 == nil {
		t.Fatalf("ir2 missing from merged input rules: %+v", merged.InputRules)
	}
	if ir2.Tier != PolicyTierWorkflow || ir2.Selector.WorkflowID != "wf-prod" {
		t.Fatalf("workflow-tier fragment rule must inherit tier/selector: %+v", *ir2)
	}
}

// TestMergeTenantPolicies_MergeSemantics pins tenant merge: topic lists append,
// MaxConcurrent takes the smaller positive bound, MCP lists merge, and tenants
// present only in extra are added.
func TestMergeTenantPolicies_MergeSemantics(t *testing.T) {
	base := map[string]TenantPolicy{
		"t1": {AllowTopics: []string{"a"}, MaxConcurrent: 5, MCP: MCPPolicy{AllowServers: []string{"s1"}}},
	}
	extra := map[string]TenantPolicy{
		"t1": {AllowTopics: []string{"b"}, MaxConcurrent: 3, MCP: MCPPolicy{AllowServers: []string{"s2"}}},
		"t2": {DenyTopics: []string{"z"}},
	}
	merged := MergeTenantPolicies(base, extra)
	t1 := merged["t1"]
	if !equalStrings(t1.AllowTopics, []string{"a", "b"}) {
		t.Fatalf("t1 AllowTopics must append base+extra: %v", t1.AllowTopics)
	}
	if t1.MaxConcurrent != 3 {
		t.Fatalf("t1 MaxConcurrent must take smaller positive bound: got %d, want 3", t1.MaxConcurrent)
	}
	if !equalStrings(t1.MCP.AllowServers, []string{"s1", "s2"}) {
		t.Fatalf("t1 MCP AllowServers must merge: %v", t1.MCP.AllowServers)
	}
	if _, ok := merged["t2"]; !ok {
		t.Fatalf("t2 (extra-only tenant) must be present")
	}
}

// TestMergeMCPPolicy_AppendsBaseThenExtra mirrors the kernel oracle
// (TestMergeMCPPolicy): base elements precede extra elements in each list.
func TestMergeMCPPolicy_AppendsBaseThenExtra(t *testing.T) {
	base := MCPPolicy{AllowServers: []string{"a"}, DenyTools: []string{"x"}}
	extra := MCPPolicy{AllowServers: []string{"b"}, DenyTools: []string{"y"}, AllowActions: []string{"read"}}
	merged := MergeMCPPolicy(base, extra)
	if !equalStrings(merged.AllowServers, []string{"a", "b"}) {
		t.Fatalf("AllowServers: %v", merged.AllowServers)
	}
	if !equalStrings(merged.DenyTools, []string{"x", "y"}) {
		t.Fatalf("DenyTools: %v", merged.DenyTools)
	}
	if !equalStrings(merged.AllowActions, []string{"read"}) {
		t.Fatalf("AllowActions: %v", merged.AllowActions)
	}
}

// TestPolicyInstalledAt pins the installed_at parser used to order fragments by
// install recency: RFC3339 -> UTC; missing/blank/garbled/non-string/non-map ->
// zero time (oldest precedence), never panicking. Unifies the kernel's
// fragmentInstalledAt and the gateway's bundleInstalledAt.
func TestPolicyInstalledAt(t *testing.T) {
	valid := map[string]any{"installed_at": "2026-02-01T00:00:00Z"}
	if got, want := PolicyInstalledAt(valid), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("valid installed_at: got %v, want %v", got, want)
	}
	zeros := []any{
		map[string]any{},                       // missing key
		map[string]any{"installed_at": "   "},  // blank
		map[string]any{"installed_at": "nope"}, // garbled
		map[string]any{"installed_at": 42},     // non-string
		"not-a-map",                            // non-map
		nil,
	}
	for i, v := range zeros {
		if got := PolicyInstalledAt(v); !got.IsZero() {
			t.Fatalf("zero-case %d must parse to zero time, got %v", i, got)
		}
	}
}

// TestCloneSafetyPolicyWithTierMetadata_ScopedDefaultMoved proves a scoped
// (non-global) fragment's DefaultDecision is relocated into TierDefaults, while
// a global fragment keeps DefaultDecision inline.
func TestCloneSafetyPolicyWithTierMetadata_ScopedDefaultMoved(t *testing.T) {
	scoped := &SafetyPolicy{Tier: PolicyTierWorkflow, Selector: PolicySelector{WorkflowID: "wf"}, DefaultDecision: "deny"}
	out := CloneSafetyPolicyWithTierMetadata(scoped)
	if out.DefaultDecision != "" {
		t.Fatalf("scoped DefaultDecision must move out of the inline field, got %q", out.DefaultDecision)
	}
	if len(out.TierDefaults) != 1 || out.TierDefaults[0].Decision != "deny" || out.TierDefaults[0].Tier != PolicyTierWorkflow {
		t.Fatalf("scoped default must move into TierDefaults: %+v", out.TierDefaults)
	}
	global := &SafetyPolicy{Tier: PolicyTierGlobal, DefaultDecision: "allow"}
	gout := CloneSafetyPolicyWithTierMetadata(global)
	if gout.DefaultDecision != "allow" {
		t.Fatalf("global DefaultDecision must stay inline, got %q", gout.DefaultDecision)
	}
}

// TestCloneSafetyPolicy_DeepIndependence proves the shared clone is deep enough
// that mutating the clone never reaches the original (top-level rule fields,
// nested Match slices, and input rules).
func TestCloneSafetyPolicy_DeepIndependence(t *testing.T) {
	orig := &SafetyPolicy{
		Rules:      []PolicyRule{{ID: "r1", Decision: "allow", Match: PolicyMatch{Topics: []string{"job.test"}}}},
		InputRules: []InputPolicyRule{{ID: "ir1", Severity: "low"}},
	}
	clone := CloneSafetyPolicy(orig)
	clone.Rules[0].Decision = "deny"
	clone.Rules[0].Match.Topics[0] = "job.evil"
	clone.InputRules[0].Severity = "critical"
	if orig.Rules[0].Decision != "allow" {
		t.Fatalf("clone shares Rules with original")
	}
	if orig.Rules[0].Match.Topics[0] != "job.test" {
		t.Fatalf("clone shares nested Match.Topics with original")
	}
	if orig.InputRules[0].Severity != "low" {
		t.Fatalf("clone shares InputRules with original")
	}
}

// TestMergeRulesByID pins the generic dedup helper directly: a duplicate id
// replaces in place (last-seen wins), empty ids always append (no dedup key),
// and an empty add slice is a no-op.
func TestMergeRulesByID(t *testing.T) {
	id := func(r PolicyRule) string { return r.ID }
	got := MergeRulesByID(
		[]PolicyRule{{ID: "a", Decision: "deny"}, {ID: "", Decision: "x"}},
		[]PolicyRule{{ID: "a", Decision: "allow"}, {ID: "", Decision: "y"}},
		id,
	)
	if len(got) != 3 {
		t.Fatalf("expected 3 rules (a replaced, two empty-id appended), got %d: %+v", len(got), got)
	}
	if got[0].ID != "a" || got[0].Decision != "allow" {
		t.Fatalf("dup id must replace in place last-seen-wins: %+v", got[0])
	}
	if got[1].Decision != "x" || got[2].Decision != "y" {
		t.Fatalf("empty-id rules must both append in order: %+v", got)
	}
	if out := MergeRulesByID([]PolicyRule{{ID: "solo"}}, nil, id); len(out) != 1 {
		t.Fatalf("empty add must be a no-op, got %d", len(out))
	}
}
