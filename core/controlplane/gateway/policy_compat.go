package gateway

// policy_compat.go provides backward-compatible aliases so that all gateway
// handler methods and tests continue to compile after policy bundle types,
// constants, and pure functions moved to the policybundles/ sub-package.

import (
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
)

// ---------- type aliases ----------

type policyBundleSnapshot = policybundles.PolicyBundleSnapshot
type policyBundleSnapshotSummary = policybundles.PolicyBundleSnapshotSummary
type policyBundleSummary = policybundles.PolicyBundleSummary
type policyBundleDetail = policybundles.PolicyBundleDetail
type policyBundleUpsertRequest = policybundles.PolicyBundleUpsertRequest
type policyPublishRequest = policybundles.PolicyPublishRequest
type policyRollbackRequest = policybundles.PolicyRollbackRequest
type outputRuleToggleRequest = policybundles.OutputRuleToggleRequest
type policyAuditEntry = policybundles.PolicyAuditEntry
type policyRuleSource = policybundles.PolicyRuleSource
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
	normalizeOutputRule          = policybundles.NormalizeOutputRule
	normalizePolicyRules         = policybundles.NormalizePolicyRules
	legacyPolicyRules            = policybundles.LegacyPolicyRules
)

// ---------- function re-exports (helpers.go) ----------

var (
	stringSliceFromAny = policybundles.StringSliceFromAny
	mergeUniqueStrings = policybundles.MergeUniqueStrings
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
	sanitizePolicyBundleValue = policybundles.SanitizePolicyBundleValue
	validateBundles          = policybundles.ValidateBundles
	resolvePublishTargets    = policybundles.ResolvePublishTargets
)

// ---------- function re-exports (merge.go) ----------

var (
	mergeSafetyPolicies   = policybundles.MergeSafetyPolicies
	cloneSafetyPolicy     = policybundles.CloneSafetyPolicy
	cloneOutputPolicyRules = policybundles.CloneOutputPolicyRules
	mergeTenantPolicies   = policybundles.MergeTenantPolicies
	cloneTenantPolicy     = policybundles.CloneTenantPolicy
	mergeMCPPolicy        = policybundles.MergeMCPPolicy
)

// ---------- function re-exports (eval.go) ----------

var (
	evaluatePolicyCheck  = policybundles.EvaluatePolicyCheck
	policyMetaFromRequest = policybundles.PolicyMetaFromRequest
	actorTypeString      = policybundles.ActorTypeString
	secretsPresent       = policybundles.SecretsPresent
	extractMCPRequest    = policybundles.ExtractMCPRequest
	pickLabel            = policybundles.PickLabel
	toProtoConstraints   = policybundles.ToProtoConstraints
	toProtoRemediations  = policybundles.ToProtoRemediations
	isConstraintsEmpty   = policybundles.IsConstraintsEmpty
	matchAny             = policybundles.MatchAny
	configMatch          = policybundles.ConfigMatch
)

// ---------- function re-exports (audit.go) ----------

var (
	auditEntryToSIEM       = policybundles.AuditEntryToSIEM
	classifyAuditAction    = policybundles.ClassifyAuditAction
	classifyAuditSeverity  = policybundles.ClassifyAuditSeverity
	policyActorID          = policybundles.PolicyActorID
	policyRole             = policybundles.PolicyRole
)
