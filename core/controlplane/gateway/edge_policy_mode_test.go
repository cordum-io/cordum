package gateway

import (
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestEdgeEvaluatePolicyModeFromBundlesUsesBoundBundleMetadata(t *testing.T) {
	session := edgecore.EdgeSession{
		PolicyMode: edgecore.PolicyModeObserve,
		Labels:     edgecore.Labels{"policy.bundle_id": "secops/edge-strict"},
	}
	bundles := map[string]any{
		"secops/edge-strict": map[string]any{
			"metadata": map[string]any{"edge_mode": "enterprise-strict"},
		},
	}

	got := edgeEvaluatePolicyModeFromBundles(session, bundles)

	if got.Mode != edgecore.PolicyModeEnterpriseStrict {
		t.Fatalf("mode = %q, want enterprise-strict", got.Mode)
	}
	if got.Source != "bundle" || got.BundleID != "secops/edge-strict" {
		t.Fatalf("source/bundle = %q/%q, want bundle/secops/edge-strict", got.Source, got.BundleID)
	}
}

func TestEdgeEvaluatePolicyModeFromBundlesFallsBackToLegacyGlobal(t *testing.T) {
	session := edgecore.EdgeSession{
		PolicyMode: edgecore.PolicyModeEnforce,
		Labels:     edgecore.Labels{"policy.bundle_id": "secops/edge-empty"},
	}
	bundles := map[string]any{
		"secops/edge-empty": map[string]any{"metadata": map[string]any{}},
	}

	got := edgeEvaluatePolicyModeFromBundles(session, bundles)

	if got.Mode != edgecore.PolicyModeEnforce {
		t.Fatalf("mode = %q, want enforce fallback", got.Mode)
	}
	if got.Source != "legacy_global" || got.BundleID != "" {
		t.Fatalf("source/bundle = %q/%q, want legacy_global/empty", got.Source, got.BundleID)
	}
}
