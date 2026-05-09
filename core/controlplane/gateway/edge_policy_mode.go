package gateway

import (
	"context"
	"fmt"
	"strings"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/policy"
)

const (
	edgePolicyModeSourceBundle       = "bundle"
	edgePolicyModeSourceLegacyGlobal = "legacy_global"
)

type edgeEvaluatePolicyModeResolution struct {
	Mode     edgecore.PolicyMode
	Source   string
	BundleID string
}

func (s *server) resolveEdgeEvaluatePolicyMode(ctx context.Context, session edgecore.EdgeSession) edgeEvaluatePolicyModeResolution {
	if edgeEvaluateBoundBundleID(session.Labels) == "" {
		return edgeEvaluatePolicyModeFromBundles(session, nil)
	}
	if s == nil || s.configSvc == nil {
		return edgeEvaluatePolicyModeFromBundles(session, nil)
	}
	bundles, _, err := s.loadPolicyBundles(ctx)
	if err != nil {
		return edgeEvaluatePolicyModeFromBundles(session, nil)
	}
	return edgeEvaluatePolicyModeFromBundles(session, bundles)
}

func edgeEvaluatePolicyModeFromBundles(session edgecore.EdgeSession, bundles map[string]any) edgeEvaluatePolicyModeResolution {
	fallback := edgeEvaluateLegacyPolicyMode(session.PolicyMode)
	bundleID := edgeEvaluateBoundBundleID(session.Labels)
	if bundleID == "" || len(bundles) == 0 {
		return edgeEvaluatePolicyModeResolution{Mode: fallback, Source: edgePolicyModeSourceLegacyGlobal}
	}
	mode, ok := edgeBundlePolicyMode(bundles[bundleID], fallback)
	if !ok {
		return edgeEvaluatePolicyModeResolution{Mode: fallback, Source: edgePolicyModeSourceLegacyGlobal}
	}
	return edgeEvaluatePolicyModeResolution{Mode: mode, Source: edgePolicyModeSourceBundle, BundleID: bundleID}
}

func edgeEvaluateLegacyPolicyMode(mode edgecore.PolicyMode) edgecore.PolicyMode {
	if mode == "" {
		return edgecore.PolicyModeObserve
	}
	return mode
}

func edgeEvaluateBoundBundleID(labels edgecore.Labels) string {
	for _, key := range []string{"policy.bundle_id", "policy_bundle_id", "bundle_id"} {
		if value := strings.TrimSpace(labels[key]); value != "" {
			return value
		}
	}
	return ""
}

func edgeBundlePolicyMode(raw any, fallback edgecore.PolicyMode) (edgecore.PolicyMode, bool) {
	bundle, ok := raw.(map[string]any)
	if !ok || bundle == nil {
		return fallback, false
	}
	rawMode := edgeBundleModeValue(bundle)
	if strings.TrimSpace(rawMode) == "" {
		return fallback, false
	}
	mode, err := policy.ParseEdgeMode(rawMode)
	if err != nil {
		return fallback, false
	}
	return edgecore.PolicyModeFromBundleMetadata(policy.Bundle{
		Metadata: policy.BundleMetadata{EdgeMode: mode},
	}, fallback), true
}

func edgeBundleModeValue(bundle map[string]any) string {
	if metadata, ok := bundle["metadata"].(map[string]any); ok {
		if raw := strings.TrimSpace(policyBundleString(metadata["edge_mode"])); raw != "" {
			return raw
		}
	}
	return strings.TrimSpace(policyBundleString(bundle["edge_mode"]))
}

func policyBundleString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
