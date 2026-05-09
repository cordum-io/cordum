package policy

// Redis key constructors for the Policy Studio bundle store
// (epic-d9a6c0a1 Backend 2). All keys live under two prefixes:
//
//	policy:bundle:*  bundle envelopes + per-bundle versions index +
//	                 per-version blobs
//	policy:scope:*   per-scope active deployment pointer + history list
//
// These prefixes are confirmed clean against existing core/* stores per
// the step-2 inventory: workflow uses `cordum:wf:*` + `runKey()`, edge
// uses `edge:session:*` + `edge:rule:*`, and registry uses
// `sys:workers:*`. None overlap with `policy:*`.
//
// Key shapes:
//
//	policy:bundle:{id}                          STRING (Bundle envelope JSON)
//	policy:bundle:{id}:versions                 ZSET   (score = unix nanos of DeployedAt; member = version string)
//	policy:bundle:{id}:version:{version}        STRING (BundleVersion JSON, includes RuleSnapshot)
//	policy:scope:{kind}:{value}:active          STRING ("{bundleID}:{version}" pointer; empty when unbound)
//	policy:scope:{kind}:{value}:history         LIST   (each element is a Deployment JSON; LPUSH on deploy/rollback; LTRIM 0 99 keeps newest 100)

const (
	bundleKeyPrefix       = "policy:bundle:"
	bundleVersionInfix    = ":version:"
	bundleVersionsSuffix  = ":versions"
	scopeKeyPrefix        = "policy:scope:"
	scopeActiveSuffix     = ":active"
	scopeHistorySuffix    = ":history"
	deploymentHistoryCap  = 100
)

// bundleKey returns the Redis key for a bundle envelope.
func bundleKey(id string) string {
	return bundleKeyPrefix + id
}

// bundleVersionKey returns the Redis key for one specific BundleVersion.
func bundleVersionKey(id, version string) string {
	return bundleKeyPrefix + id + bundleVersionInfix + version
}

// bundleVersionsIndexKey returns the Redis key for the ZSET that indexes
// every version belonging to a bundle.
func bundleVersionsIndexKey(id string) string {
	return bundleKeyPrefix + id + bundleVersionsSuffix
}

// scopeActiveKey returns the Redis key for the active-deployment pointer
// of a scope. The stored value is the literal "{bundleID}:{version}".
// For RuleScopeGlobal the Value field is empty; the helper still emits a
// deterministic key.
func scopeActiveKey(scope RuleScope) string {
	return scopeKeyPrefix + string(scope.Kind) + ":" + scope.Value + scopeActiveSuffix
}

// scopeDeploymentHistoryKey returns the Redis key for the LIST that
// stores a scope's deploy/rollback history.
func scopeDeploymentHistoryKey(scope RuleScope) string {
	return scopeKeyPrefix + string(scope.Kind) + ":" + scope.Value + scopeHistorySuffix
}
