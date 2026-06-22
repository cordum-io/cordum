package policybundles

import (
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

// TestBuildPolicyFromBundles_DuplicateRuleIDRecencyWins reproduces the
// replay/evals-vs-kernel divergence: two co-installed bundles define the SAME
// rule id with DIFFERENT decisions, and the alphabetically-first bundle is the
// OLDER one. The authoritative kernel merge orders by installed_at ASC and
// dedupes rule ids last-seen-wins, so the most-recently-installed bundle wins.
// The gateway merge (replay/evals/tier-evidence) must produce the SAME result.
func TestBuildPolicyFromBundles_DuplicateRuleIDRecencyWins(t *testing.T) {
	bundles := map[string]any{
		// Alpha-FIRST bundle, installed EARLIER, decision deny.
		"aaa-pack/policy": map[string]any{
			"installed_at": "2026-01-01T00:00:00Z",
			"content": `version: "1"
rules:
  - id: dup-rule
    match:
      topics:
        - job.test
    decision: deny
    reason: older bundle deny
`,
		},
		// Alpha-LAST bundle, installed LATER, decision allow.
		"zzz-pack/policy": map[string]any{
			"installed_at": "2026-02-01T00:00:00Z",
			"content": `version: "1"
rules:
  - id: dup-rule
    match:
      topics:
        - job.test
    decision: allow
    reason: newer bundle allow
`,
		},
	}

	merged, _, err := BuildPolicyFromBundles(bundles)
	if err != nil {
		t.Fatalf("BuildPolicyFromBundles: %v", err)
	}
	// Kernel semantics: the duplicate id dedupes to ONE rule and the
	// most-recently-installed bundle (zzz, installed 2026-02) wins. Today the
	// gateway keeps BOTH (alpha order, no dedup) so first-match returns the
	// older 'deny' — disagreeing with production.
	ids := ruleIDs(merged.Rules)
	if len(ids) != 1 {
		t.Fatalf("duplicate rule id must dedupe to 1 rule (recency wins), got %v", ids)
	}
	if merged.Rules[0].Decision != "allow" {
		t.Fatalf("most-recently-installed bundle must win the duplicate id: decision=%q, want allow", merged.Rules[0].Decision)
	}
}

// recencyBundle builds a single-rule bundle (rule id `dup-rule`) with the given
// decision and installed_at, for the install-order matrix.
func recencyBundle(decision, installedAt string) map[string]any {
	return map[string]any{
		"installed_at": installedAt,
		"content": `version: "1"
rules:
  - id: dup-rule
    match:
      topics:
        - job.test
    decision: ` + decision + `
    reason: r
`,
	}
}

// TestBuildPolicyFromBundles_RecencyAcrossInstallOrders asserts the winner of a
// duplicate rule id follows installed_at recency regardless of bundle-key alpha
// order (so whichever bundle is newer wins, even the alphabetically-first one).
func TestBuildPolicyFromBundles_RecencyAcrossInstallOrders(t *testing.T) {
	cases := []struct {
		name                       string
		aaaAt, zzzAt               string
		aaaDec, zzzDec, wantWinner string
	}{
		{"alpha_last_is_newer", "2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z", "deny", "allow", "allow"},
		{"alpha_first_is_newer", "2026-03-01T00:00:00Z", "2026-02-01T00:00:00Z", "deny", "allow", "deny"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			merged, _, err := BuildPolicyFromBundles(map[string]any{
				"aaa-pack/policy": recencyBundle(tc.aaaDec, tc.aaaAt),
				"zzz-pack/policy": recencyBundle(tc.zzzDec, tc.zzzAt),
			})
			if err != nil {
				t.Fatalf("BuildPolicyFromBundles: %v", err)
			}
			if ids := ruleIDs(merged.Rules); len(ids) != 1 {
				t.Fatalf("expected 1 deduped rule, got %v", ids)
			}
			if merged.Rules[0].Decision != tc.wantWinner {
				t.Fatalf("recency winner decision=%q, want %q", merged.Rules[0].Decision, tc.wantWinner)
			}
		})
	}
}

// TestBuildPolicyFromBundles_EqualInstalledAtAlphaTiebreak asserts that when
// installed_at is equal, the bundle-key alphabetical tiebreak decides (later key
// merges last and wins) — a total order that never relies on map iteration.
func TestBuildPolicyFromBundles_EqualInstalledAtAlphaTiebreak(t *testing.T) {
	merged, _, err := BuildPolicyFromBundles(map[string]any{
		"aaa-pack/policy": recencyBundle("deny", "2026-01-01T00:00:00Z"),
		"zzz-pack/policy": recencyBundle("allow", "2026-01-01T00:00:00Z"),
	})
	if err != nil {
		t.Fatalf("BuildPolicyFromBundles: %v", err)
	}
	if ids := ruleIDs(merged.Rules); len(ids) != 1 {
		t.Fatalf("expected 1 deduped rule, got %v", ids)
	}
	if merged.Rules[0].Decision != "allow" {
		t.Fatalf("equal installed_at must use alpha tiebreak (zzz wins): decision=%q, want allow", merged.Rules[0].Decision)
	}
}

// TestBuildPolicyFromBundles_SnapshotStableAcrossInstallOrder asserts the
// cfg:<sha> snapshot is a function of the content SET only (install order
// independent), so a reinstall that only bumps installed_at does not churn it.
func TestBuildPolicyFromBundles_SnapshotStableAcrossInstallOrder(t *testing.T) {
	content := map[string]any{"content": `version: "1"
rules:
  - id: r1
    match:
      topics:
        - job.test
    decision: deny
    reason: r
`}
	build := func(installedAt string) string {
		aaa := map[string]any{"installed_at": installedAt, "content": content["content"]}
		zzz := map[string]any{"installed_at": "2026-05-05T00:00:00Z", "content": content["content"]}
		_, snap, err := BuildPolicyFromBundles(map[string]any{"aaa-pack/policy": aaa, "zzz-pack/policy": zzz})
		if err != nil {
			t.Fatalf("BuildPolicyFromBundles: %v", err)
		}
		return snap
	}
	if s1, s2 := build("2026-01-01T00:00:00Z"), build("2026-09-09T00:00:00Z"); s1 != s2 {
		t.Fatalf("snapshot must be install-order-independent (content-set only): %q != %q", s1, s2)
	}
}

// TestBuildPolicyFromBundles_ConflictingDuplicateWinnerFlipsButSnapshotStable
// documents the install-order-DEPENDENT conflict-resolution contract that the
// identical-content snapshot test above cannot exercise: two co-installed bundles
// carry the SAME rule id (dup-rule) with DIFFERENT decisions, so the merge winner
// follows installed_at recency and FLIPS when the timestamps are swapped — while
// the cfg:<sha> snapshot folds only the {key,content} SET and therefore stays
// IDENTICAL across the swap. That divergence (same cache key, different enforced
// decision) is the stale-cache hazard: a reinstall that only bumps installed_at
// can flip the winning rule without churning the snapshot, so any cache keyed on
// cfg:<sha> alone must be invalidated on install-order changes too.
func TestBuildPolicyFromBundles_ConflictingDuplicateWinnerFlipsButSnapshotStable(t *testing.T) {
	// Order A: aaa older (deny) / zzz newer (allow) -> newer zzz wins -> allow.
	mergedA, snapA, err := BuildPolicyFromBundles(map[string]any{
		"aaa-pack/policy": recencyBundle("deny", "2026-01-01T00:00:00Z"),
		"zzz-pack/policy": recencyBundle("allow", "2026-02-01T00:00:00Z"),
	})
	if err != nil {
		t.Fatalf("BuildPolicyFromBundles order A: %v", err)
	}
	// Order B: timestamps swapped so aaa is now newer (deny) / zzz older (allow)
	// -> newer aaa wins -> deny. Same content SET, only installed_at flipped.
	mergedB, snapB, err := BuildPolicyFromBundles(map[string]any{
		"aaa-pack/policy": recencyBundle("deny", "2026-02-01T00:00:00Z"),
		"zzz-pack/policy": recencyBundle("allow", "2026-01-01T00:00:00Z"),
	})
	if err != nil {
		t.Fatalf("BuildPolicyFromBundles order B: %v", err)
	}

	// Each order dedupes the conflicting id to exactly one rule.
	if ids := ruleIDs(mergedA.Rules); len(ids) != 1 || ids[0] != "dup-rule" {
		t.Fatalf("order A must dedupe to single dup-rule, got %v", ids)
	}
	if ids := ruleIDs(mergedB.Rules); len(ids) != 1 || ids[0] != "dup-rule" {
		t.Fatalf("order B must dedupe to single dup-rule, got %v", ids)
	}

	// Winner is install-order DEPENDENT: concrete decisions, flipped by recency.
	if mergedA.Rules[0].Decision != "allow" {
		t.Fatalf("order A winner decision=%q, want allow (zzz installed later)", mergedA.Rules[0].Decision)
	}
	if mergedB.Rules[0].Decision != "deny" {
		t.Fatalf("order B winner decision=%q, want deny (aaa installed later)", mergedB.Rules[0].Decision)
	}
	if mergedA.Rules[0].Decision == mergedB.Rules[0].Decision {
		t.Fatalf("conflicting duplicate-id winner must flip with install order, both=%q", mergedA.Rules[0].Decision)
	}

	// Snapshot is install-order INDEPENDENT (content SET only): identical across
	// the swap even though the enforced decision differs — the stale-cache hazard.
	if snapA == "" || snapB == "" {
		t.Fatalf("expected non-empty snapshots, got %q / %q", snapA, snapB)
	}
	if snapA != snapB {
		t.Fatalf("snapshot must stay stable across install order (content-set only): %q != %q", snapA, snapB)
	}
}

// TestMergeSafetyPolicies_DedupesInputAndOutputRules covers the two rule sets
// the old gateway merge mishandled: InputRules were dropped entirely and
// OutputRules were appended without dedup.
func TestMergeSafetyPolicies_DedupesInputAndOutputRules(t *testing.T) {
	base := &config.SafetyPolicy{
		InputRules:  []config.InputPolicyRule{{ID: "in1", Decision: "deny"}},
		OutputRules: []config.OutputPolicyRule{{ID: "out1", Decision: "quarantine"}},
	}
	extra := &config.SafetyPolicy{
		InputRules:  []config.InputPolicyRule{{ID: "in1", Decision: "require_approval"}},
		OutputRules: []config.OutputPolicyRule{{ID: "out1", Decision: "redact"}},
	}
	merged := MergeSafetyPolicies(base, extra)
	if len(merged.InputRules) != 1 || merged.InputRules[0].Decision != "require_approval" {
		t.Fatalf("input rule must dedupe last-seen-wins (and not be dropped), got %+v", merged.InputRules)
	}
	if len(merged.OutputRules) != 1 || merged.OutputRules[0].Decision != "redact" {
		t.Fatalf("output rule must dedupe last-seen-wins, got %+v", merged.OutputRules)
	}
}

// TestBuildPolicyFromBundles_SingleBundleUnchanged confirms the no-duplicate
// single-bundle path keeps rule order/content unchanged.
func TestBuildPolicyFromBundles_SingleBundleUnchanged(t *testing.T) {
	merged, _, err := BuildPolicyFromBundles(map[string]any{
		"solo-pack/policy": map[string]any{
			"installed_at": "2026-01-01T00:00:00Z",
			"content": `version: "1"
rules:
  - id: a
    match:
      topics:
        - job.test
    decision: deny
    reason: r
  - id: b
    match:
      topics:
        - job.other
    decision: allow
    reason: r
`,
		},
	})
	if err != nil {
		t.Fatalf("BuildPolicyFromBundles: %v", err)
	}
	if ids := ruleIDs(merged.Rules); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("single-bundle order/content must be unchanged, got %v", ids)
	}
}

// TestMergeSafetyPolicies_OutputPolicyAnyEnable locks the gateway compiler's
// any-enable OutputPolicy semantics (task-198acafd) — now the SINGLE canonical
// behavior shared with the kernel via config.MergePolicies. A fragment that
// enables output scanning keeps it enabled across the merge; FailMode fills
// first-non-empty with base precedence.
func TestMergeSafetyPolicies_OutputPolicyAnyEnable(t *testing.T) {
	base := &config.SafetyPolicy{OutputPolicy: config.OutputPolicyConfig{Enabled: false, FailMode: ""}}
	extra := &config.SafetyPolicy{OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "closed"}}
	merged := MergeSafetyPolicies(base, extra)
	if !merged.OutputPolicy.Enabled || merged.OutputPolicy.FailMode != "closed" {
		t.Fatalf("gateway merge must OR-enable + take first non-empty FailMode, got %+v", merged.OutputPolicy)
	}
}
