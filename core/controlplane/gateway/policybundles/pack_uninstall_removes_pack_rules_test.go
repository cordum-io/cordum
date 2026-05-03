package policybundles

import (
	"testing"
)

// TestPackUninstallRemovesPackRulesPreservesStudioAndInvariants pins
// the deterministic-rebuild contract for pack uninstall (DoD #3 "Pack
// install merges into the appropriate Global section; uninstall removes
// them" + task rail "Pack-contributed rules MUST be removable when a
// pack is uninstalled").
//
// handlers_packs.go removePolicyOverlay does `delete(bundles,
// fragmentID)` on the policy_bundles config doc. The next call to
// BuildPolicyFromBundles must yield a merged policy WITHOUT the pack's
// rules, while studio + invariants bundles (different keys, never
// touched by pack uninstall) survive unchanged.
func TestPackUninstallRemovesPackRulesPreservesStudioAndInvariants(t *testing.T) {
	studioBundle := map[string]any{"content": `version: "1"
rules:
  - id: studio-allow-base
    match:
      topics: [job.test]
    decision: allow
`}
	invariantsBundle := map[string]any{"content": `version: "1"
rules:
  - id: inv-deny-secret-paths
    match:
      labels:
        path.class: secret
    decision: deny
`}

	// Phase 1: studio + invariants + pack all installed.
	withPack := map[string]any{
		"secops/global":             studioBundle,
		PolicyInvariantsBundleKey:   invariantsBundle,
		"pack/example/policy": map[string]any{"content": `version: "1"
rules:
  - id: pack-allow-secret-read
    match:
      topics: [job.edge.action]
      labels:
        path.class: secret
    decision: allow
`},
	}
	merged1, snap1, err := BuildPolicyFromBundles(withPack)
	if err != nil {
		t.Fatalf("phase 1 build: %v", err)
	}
	got1 := ruleIDs(merged1.Rules)
	for _, want := range []string{"studio-allow-base", "pack-allow-secret-read", "inv-deny-secret-paths"} {
		if !sliceContains(got1, want) {
			t.Fatalf("phase 1 missing rule %q; got %v", want, got1)
		}
	}

	// Phase 2: pack uninstall — delete the pack key from the bundles
	// map (mirror of handlers_packs.go removePolicyOverlay).
	delete(withPack, "pack/example/policy")
	merged2, snap2, err := BuildPolicyFromBundles(withPack)
	if err != nil {
		t.Fatalf("phase 2 build: %v", err)
	}
	got2 := ruleIDs(merged2.Rules)

	// Pack rule MUST be gone.
	if sliceContains(got2, "pack-allow-secret-read") {
		t.Fatalf("phase 2 must NOT contain pack rule; got %v", got2)
	}
	// Studio + invariants survive.
	for _, want := range []string{"studio-allow-base", "inv-deny-secret-paths"} {
		if !sliceContains(got2, want) {
			t.Fatalf("phase 2 missing surviving rule %q; got %v", want, got2)
		}
	}
	// Snapshot must change — downstream caches are forced to refresh.
	if snap1 == snap2 {
		t.Fatalf("snapshot must change after pack uninstall; %q == %q", snap1, snap2)
	}
}

// TestPackUninstallDoesNotTouchInvariantsBundleKey pins the security
// invariant: pack uninstall MUST NEVER remove the studio-invariants
// bundle. The bundle key is held under PolicyStudioPrefix ("secops/")
// and pack uninstall only deletes by pack overlay's FragmentID — it
// never iterates the secops/* namespace.
func TestPackUninstallDoesNotTouchInvariantsBundleKey(t *testing.T) {
	bundles := map[string]any{
		PolicyInvariantsBundleKey: map[string]any{"content": `version: "1"
rules:
  - id: inv-deny-secret-paths
    match:
      labels:
        path.class: secret
    decision: deny
`},
		"pack/example/policy": map[string]any{"content": `version: "1"
rules:
  - id: pack-rule
    match:
      topics: [job.test]
    decision: allow
`},
	}

	// Simulate uninstalling EVERY pack-named key — the invariants
	// bundle (under secops/ prefix, NOT pack/ prefix) must survive.
	for key := range bundles {
		if !startsWithPackPrefix(key) {
			continue
		}
		delete(bundles, key)
	}

	merged, _, err := BuildPolicyFromBundles(bundles)
	if err != nil {
		t.Fatalf("BuildPolicyFromBundles: %v", err)
	}
	got := ruleIDs(merged.Rules)
	if !sliceContains(got, "inv-deny-secret-paths") {
		t.Fatalf("invariant rule must survive total pack uninstall; got %v", got)
	}
	if sliceContains(got, "pack-rule") {
		t.Fatalf("pack rule must be gone; got %v", got)
	}
}

// startsWithPackPrefix mirrors the heuristic the dashboard uses to
// classify a bundle as pack-sourced (BundleSummaryList in bundles.go
// L66+ uses "secops/" prefix vs "/" inclusion). For the test, a key
// containing "/" but not under PolicyStudioPrefix is treated as pack.
func startsWithPackPrefix(key string) bool {
	if len(key) == 0 {
		return false
	}
	if len(key) >= len(PolicyStudioPrefix) && key[:len(PolicyStudioPrefix)] == PolicyStudioPrefix {
		return false
	}
	for _, c := range key {
		if c == '/' {
			return true
		}
	}
	return false
}
