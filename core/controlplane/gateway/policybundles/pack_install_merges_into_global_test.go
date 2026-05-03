package policybundles

import (
	"strings"
	"testing"
)

// TestPackInstallMergesIntoGlobal pins DoD #3 — pack-contributed rules
// flow into the unified Global policy via BuildPolicyFromBundles. When
// a pack overlay lands in the bundles map (handlers_packs.go
// applyPolicyOverlay does `bundles[fragmentID] = {...}`), the next call
// to BuildPolicyFromBundles yields a merged SafetyPolicy that includes
// the pack's rules ALONGSIDE existing studio rules — there is no
// parallel pack policy pool. This is the core "one Global authority"
// claim from the EDGE-052 task definition.
func TestPackInstallMergesIntoGlobal(t *testing.T) {
	const studioContent = `version: "1"
default_decision: deny
rules:
  - id: studio-allow-base
    match:
      topics: [job.test]
    decision: allow
`
	const packContent = `version: "1"
rules:
  - id: pack-allow-edge-action
    match:
      topics: [job.edge.action]
      capabilities: [exec.shell]
    decision: allow
  - id: pack-deny-edge-secret
    match:
      topics: [job.edge.action]
      labels:
        path.class: secret
    decision: deny
`

	// Pre-install state: studio bundle only.
	bundlesPre := map[string]any{
		"secops/global": map[string]any{"content": studioContent},
	}
	mergedPre, snapPre, err := BuildPolicyFromBundles(bundlesPre)
	if err != nil {
		t.Fatalf("pre-install BuildPolicyFromBundles: %v", err)
	}
	if got := ruleIDs(mergedPre.Rules); len(got) != 1 || got[0] != "studio-allow-base" {
		t.Fatalf("pre-install rules = %v, want [studio-allow-base]", got)
	}

	// Simulate pack install: handlers_packs.go applyPolicyOverlay
	// writes bundles[<fragment-id>] = {content: ..., sha256: ...}.
	bundlesPost := map[string]any{
		"secops/global": map[string]any{"content": studioContent},
		"pack/example/policy": map[string]any{
			"content": packContent,
			"sha256":  "abc",
			"version": "v1.0.0",
		},
	}
	mergedPost, snapPost, err := BuildPolicyFromBundles(bundlesPost)
	if err != nil {
		t.Fatalf("post-install BuildPolicyFromBundles: %v", err)
	}

	// Pack rules must appear ALONGSIDE studio rules — not in a
	// parallel pool.
	got := ruleIDs(mergedPost.Rules)
	wantSubset := []string{"studio-allow-base", "pack-allow-edge-action", "pack-deny-edge-secret"}
	for _, want := range wantSubset {
		if !sliceContains(got, want) {
			t.Fatalf("post-install rules missing %q; got %v", want, got)
		}
	}

	// Snapshot must change so downstream caches (EDGE-018, MCP gate
	// cache, dashboard view) re-fetch.
	if snapPre == snapPost {
		t.Fatalf("snapshot must change on pack install; %q == %q", snapPre, snapPost)
	}
	if !strings.HasPrefix(snapPost, "cfg:") {
		t.Fatalf("expected cfg:-prefixed snapshot, got %q", snapPost)
	}
}

// TestPackInstallNoPackBundlesIsStable confirms repeated calls to
// BuildPolicyFromBundles with the same bundle map yield the same
// snapshot — pack install changes the snapshot, no-op installs do not.
func TestPackInstallNoPackBundlesIsStable(t *testing.T) {
	bundles := map[string]any{
		"secops/global": map[string]any{"content": `version: "1"
rules:
  - id: studio-allow
    match:
      topics: [job.test]
    decision: allow
`},
	}
	_, snap1, err := BuildPolicyFromBundles(bundles)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	_, snap2, err := BuildPolicyFromBundles(bundles)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if snap1 != snap2 {
		t.Fatalf("identical bundles produced different snapshots: %q vs %q", snap1, snap2)
	}
}

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
