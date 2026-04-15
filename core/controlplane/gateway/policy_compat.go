package gateway

// policy_compat.go provides backward-compatible aliases so that all gateway
// handler methods and tests continue to compile after policy bundle types,
// constants, and pure functions moved to the policybundles/ sub-package.

import (
	"context"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
)

// ---------- type aliases ----------

type policyBundleSnapshot = policybundles.PolicyBundleSnapshot
type policyBundleSnapshotSummary = policybundles.PolicyBundleSnapshotSummary
type policyBundleDetail = policybundles.PolicyBundleDetail
type policyBundleUpsertRequest = policybundles.PolicyBundleUpsertRequest
type policyPublishRequest = policybundles.PolicyPublishRequest
type policyRollbackRequest = policybundles.PolicyRollbackRequest
type outputRuleToggleRequest = policybundles.OutputRuleToggleRequest
type policyAuditEntry = policybundles.PolicyAuditEntry
type policyRuleParseError = policybundles.PolicyRuleParseError

// ---------- constant aliases ----------

const (
	policySnapshotsScope = policybundles.PolicySnapshotsScope
	policySnapshotsID    = policybundles.PolicySnapshotsID
	policySnapshotsKey   = policybundles.PolicySnapshotsKey
	policyAuditScope     = policybundles.PolicyAuditScope
	policyAuditID        = policybundles.PolicyAuditID
	policyAuditKey       = policybundles.PolicyAuditKey
	policyStudioPrefix   = policybundles.PolicyStudioPrefix
)

// ---------- function re-exports (rules.go) ----------

var (
	rulesFromPolicyContent       = policybundles.RulesFromPolicyContent
	outputRulesFromPolicyContent = policybundles.OutputRulesFromPolicyContent
	legacyPolicyRules            = policybundles.LegacyPolicyRules
)

// ---------- function re-exports (helpers.go) ----------

var (
	stringSliceFromAny = policybundles.StringSliceFromAny
	stringFromAny      = policybundles.StringFromAny
)

// ---------- function re-exports (bundles.go) ----------

var (
	bundleIDFromRequest      = policybundles.BundleIDFromRequest
	bundleSummaryList        = policybundles.BundleSummaryList
	bundleEnabled            = policybundles.BundleEnabled
	cloneBundleMap           = policybundles.CloneBundleMap
	buildPolicyFromBundles   = policybundles.BuildPolicyFromBundles
	policyBundleContent      = policybundles.PolicyBundleContent
	sanitizePolicyBundleYAML = policybundles.SanitizePolicyBundleYAML
	validateBundles          = policybundles.ValidateBundles
	resolvePublishTargets    = policybundles.ResolvePublishTargets
)

// ---------- function re-exports (merge.go) ----------

var (
	mergeSafetyPolicies = policybundles.MergeSafetyPolicies
	mergeTenantPolicies = policybundles.MergeTenantPolicies
	mergeMCPPolicy      = policybundles.MergeMCPPolicy
)

// ---------- function re-exports (eval.go) ----------

var (
	evaluatePolicyCheck = policybundles.EvaluatePolicyCheck
	pickLabel           = policybundles.PickLabel
	toProtoConstraints  = policybundles.ToProtoConstraints
	matchAny            = policybundles.MatchAny
)

// ---------- function re-exports (audit.go) ----------

var (
	auditEntryToSIEM = policybundles.AuditEntryToSIEM
	policyActorID    = policybundles.PolicyActorID
	policyRole       = policybundles.PolicyRole
)

// resolveAgentForAudit looks up agent identity from the agent store by ID.
// Returns (agentID, agentName, agentRiskTier). For unlinked or missing agents,
// returns ("unlinked", "unlinked", "").
func (s *server) resolveAgentForAudit(ctx context.Context, agentID string) (string, string, string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "unlinked", "unlinked", ""
	}
	if s.agentIdentityStore == nil {
		return agentID, agentID, ""
	}
	agent, err := s.agentIdentityStore.Get(ctx, agentID)
	if err != nil || agent == nil {
		return agentID, agentID, ""
	}
	return agent.ID, agent.Name, agent.RiskTier
}
