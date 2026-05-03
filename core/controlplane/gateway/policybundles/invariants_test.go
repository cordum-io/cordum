package policybundles

import (
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

func TestInvariantDenyBeatsPackAllow(t *testing.T) {
	bundles := map[string]any{
		"pack/example/policy": map[string]any{
			"content": `version: "1"
default_decision: deny
rules:
  - id: pack-allow-secret-read
    match:
      topics:
        - job.edge.action
      labels:
        path.class: secret
    decision: allow
    reason: pack tries to allow secret read
`,
		},
		PolicyInvariantsBundleKey: map[string]any{
			"content": `version: "1"
rules:
  - id: inv-deny-secret-paths
    match:
      topics:
        - job.edge.action
      labels:
        path.class: secret
    decision: deny
    reason: SecOps invariant — secret paths
`,
		},
	}

	merged, snap, err := BuildPolicyFromBundles(bundles)
	if err != nil {
		t.Fatalf("BuildPolicyFromBundles: %v", err)
	}
	if !strings.HasPrefix(snap, "cfg:") {
		t.Fatalf("snapshot must be cfg:-prefixed, got %q", snap)
	}
	if merged == nil || len(merged.Rules) != 2 {
		t.Fatalf("expected exactly 2 merged rules, got %+v", ruleIDs(merged.Rules))
	}
	// SECURITY FLOOR: invariant DENY MUST be first so a first-match
	// evaluator returns DENY before ever consulting pack-allow-secret-read.
	if merged.Rules[0].ID != "inv-deny-secret-paths" {
		t.Fatalf("invariant DENY must be at front of merged.Rules; got order: %v",
			ruleIDs(merged.Rules))
	}
	if merged.Rules[0].Decision != "deny" {
		t.Fatalf("expected deny decision on first rule; got %q", merged.Rules[0].Decision)
	}
}

func TestInvariantAllowYieldsToExplicitPackDeny(t *testing.T) {
	bundles := map[string]any{
		"pack/example/policy": map[string]any{
			"content": `version: "1"
rules:
  - id: pack-deny-job-test
    match:
      topics:
        - job.test
    decision: deny
    reason: pack-authored deny
`,
		},
		PolicyInvariantsBundleKey: map[string]any{
			"content": `version: "1"
rules:
  - id: inv-allow-default
    match:
      topics:
        - job.test
    decision: allow
    reason: invariant default allow fallback
`,
		},
	}

	merged, _, err := BuildPolicyFromBundles(bundles)
	if err != nil {
		t.Fatalf("BuildPolicyFromBundles: %v", err)
	}
	ids := ruleIDs(merged.Rules)
	if len(ids) != 2 {
		t.Fatalf("expected 2 rules, got %v", ids)
	}
	// Explicit DENY (any source) must precede invariant ALLOW so first-match
	// evaluators short-circuit on DENY before the ALLOW fallback.
	if ids[0] != "pack-deny-job-test" {
		t.Fatalf("expected explicit DENY first to override invariant ALLOW; got %v", ids)
	}
	if ids[len(ids)-1] != "inv-allow-default" {
		t.Fatalf("expected invariant ALLOW to be last (default fallback); got %v", ids)
	}
}

func TestPackUninstallRemovesPackRulesPreservesInvariants(t *testing.T) {
	invariantsBundle := map[string]any{
		"content": `version: "1"
rules:
  - id: inv-deny-secret-paths
    match:
      topics:
        - job.edge.action
      labels:
        path.class: secret
    decision: deny
    reason: SecOps invariant — secret paths
`,
	}
	bundlesWithPack := map[string]any{
		"pack/example/policy": map[string]any{
			"content": `version: "1"
rules:
  - id: pack-allow-secret-read
    match:
      topics:
        - job.edge.action
    decision: allow
`,
		},
		PolicyInvariantsBundleKey: invariantsBundle,
	}
	merged1, snap1, err := BuildPolicyFromBundles(bundlesWithPack)
	if err != nil {
		t.Fatalf("with pack: %v", err)
	}
	if got := ruleIDs(merged1.Rules); len(got) != 2 {
		t.Fatalf("expected 2 rules with pack installed, got %v", got)
	}

	// Simulate pack uninstall by removing the pack key — handlers_packs.go
	// removePolicyOverlay does exactly this `delete(bundles, fragmentID)`.
	bundlesWithoutPack := map[string]any{
		PolicyInvariantsBundleKey: invariantsBundle,
	}
	merged2, snap2, err := BuildPolicyFromBundles(bundlesWithoutPack)
	if err != nil {
		t.Fatalf("without pack: %v", err)
	}
	got := ruleIDs(merged2.Rules)
	if len(got) != 1 || got[0] != "inv-deny-secret-paths" {
		t.Fatalf("expected only invariant rule after uninstall; got %v", got)
	}
	if snap1 == snap2 {
		t.Fatalf("snapshot must change after pack uninstall (cache invalidation): %s", snap1)
	}
}

func TestInvariantSnapshotChangeBumpsCfgHash(t *testing.T) {
	base := map[string]any{
		"secops/global": map[string]any{
			"content": `version: "1"
rules:
  - id: studio-allow-test
    match:
      topics:
        - job.test
    decision: allow
`,
		},
	}
	_, snapNoInv, err := BuildPolicyFromBundles(base)
	if err != nil {
		t.Fatalf("snapNoInv: %v", err)
	}

	withInv := map[string]any{
		"secops/global": base["secops/global"],
		PolicyInvariantsBundleKey: map[string]any{
			"content": `version: "1"
rules:
  - id: inv-deny-test
    match:
      topics:
        - job.test
    decision: deny
`,
		},
	}
	_, snapWithInv, err := BuildPolicyFromBundles(withInv)
	if err != nil {
		t.Fatalf("snapWithInv: %v", err)
	}

	if snapNoInv == snapWithInv {
		t.Fatalf("adding invariants bundle must change snapshot hash: %q == %q",
			snapNoInv, snapWithInv)
	}

	// Same invariants content + same other bundles must produce a stable hash.
	_, snapWithInv2, err := BuildPolicyFromBundles(withInv)
	if err != nil {
		t.Fatalf("snapWithInv2: %v", err)
	}
	if snapWithInv != snapWithInv2 {
		t.Fatalf("identical bundles must produce identical snapshot hashes: %q vs %q",
			snapWithInv, snapWithInv2)
	}
}

func TestSplitInvariantsFromBundles_ParsesRulesAndOutputRules(t *testing.T) {
	bundles := map[string]any{
		PolicyInvariantsBundleKey: map[string]any{
			"content": `version: "1"
rules:
  - id: inv-deny-secret
    match:
      topics:
        - job.edge.action
    decision: deny
output_rules:
  - id: inv-quarantine-api-key
    severity: high
    match:
      content_patterns:
        - 'API_KEY=[A-Za-z0-9]+'
    decision: quarantine
`,
		},
	}
	rules, outputRules, err := SplitInvariantsFromBundles(bundles)
	if err != nil {
		t.Fatalf("SplitInvariantsFromBundles: %v", err)
	}
	if len(rules) != 1 || rules[0].ID != "inv-deny-secret" {
		t.Fatalf("expected one invariant rule, got %+v", rules)
	}
	if len(outputRules) != 1 || outputRules[0].ID != "inv-quarantine-api-key" {
		t.Fatalf("expected one invariant output rule, got %+v", outputRules)
	}
	if outputRules[0].Decision != "quarantine" {
		t.Fatalf("expected quarantine decision, got %q", outputRules[0].Decision)
	}
}

func TestSplitInvariantsFromBundles_AbsentBundleIsNil(t *testing.T) {
	rules, outputRules, err := SplitInvariantsFromBundles(map[string]any{
		"secops/global": map[string]any{"content": "version: \"1\"\n"},
	})
	if err != nil {
		t.Fatalf("SplitInvariantsFromBundles: %v", err)
	}
	if rules != nil {
		t.Fatalf("expected nil rules when invariants bundle absent, got %+v", rules)
	}
	if outputRules != nil {
		t.Fatalf("expected nil output rules when invariants bundle absent, got %+v", outputRules)
	}
}

func ruleIDs(rs []config.PolicyRule) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}
