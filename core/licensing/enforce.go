package licensing

import "strings"

const DefaultUpgradeURL = "https://cordum.io/pricing"

type TierLimitHTTPError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Limit      string `json:"limit"`
	Current    int64  `json:"current"`
	Allowed    int64  `json:"allowed"`
	UpgradeURL string `json:"upgrade_url"`
}

func CheckWorkerLimit(current int64, entitlements Entitlements) *TierLimitError {
	return checkNumericLimit("max_workers", current, entitlements.MaxWorkers)
}

func CheckJobConcurrency(current int64, entitlements Entitlements) *TierLimitError {
	return checkNumericLimit("max_concurrent_jobs", current, entitlements.MaxConcurrentJobs)
}

func CheckWorkflowSteps(current int64, entitlements Entitlements) *TierLimitError {
	return checkNumericLimit("max_workflow_steps", current, effectiveLimit(entitlements.MaxWorkflowSteps, entitlements.Limits, "max_workflow_steps"))
}

func CheckActiveWorkflows(current int64, entitlements Entitlements) *TierLimitError {
	return checkNumericLimit("max_active_workflows", current, effectiveLimit(entitlements.MaxActiveWorkflows, entitlements.Limits, "max_active_workflows"))
}

func CheckApprovalMode(requested, allowed string) *TierLimitError {
	requestedMode := ParseApprovalMode(requested)
	allowedMode := ParseApprovalMode(allowed)
	if approvalModeRank(string(requestedMode)) <= approvalModeRank(string(allowedMode)) {
		return nil
	}
	return newTierLimitError("approval_mode", int64(approvalModeRank(string(requestedMode))), int64(approvalModeRank(string(allowedMode))))
}

func CheckPolicyBundleLimit(current int64, entitlements Entitlements) *TierLimitError {
	return checkNumericLimit("max_policy_bundles", current, effectiveLimit(entitlements.MaxPolicyBundles, entitlements.Limits, "max_policy_bundles"))
}

func CheckSchemaCount(current int64, entitlements Entitlements) *TierLimitError {
	return checkNumericLimit("max_schema_count", current, effectiveLimit(entitlements.MaxSchemaCount, entitlements.Limits, "max_schema_count", "max_schemas"))
}

func CheckRateLimitRPS(current int64, entitlements Entitlements) *TierLimitError {
	return checkNumericLimit("requests_per_second", current, effectiveLimit(entitlements.RequestsPerSecond, entitlements.Limits, "requests_per_second", "rate_limit_rps", "max_requests_per_second"))
}

func CheckArtifactSize(current int64, entitlements Entitlements) *TierLimitError {
	allowed := entitlements.MaxArtifactBytes
	if allowed == 0 {
		allowed = entitlements.MaxBodyBytes
	}
	return checkNumericLimit("max_artifact_bytes", current, effectiveLimit(allowed, entitlements.Limits, "max_artifact_bytes"))
}

func (e *TierLimitError) ToHTTPError() TierLimitHTTPError {
	if e == nil {
		return TierLimitHTTPError{}
	}
	upgradeURL := strings.TrimSpace(e.UpgradeURL)
	if upgradeURL == "" {
		upgradeURL = DefaultUpgradeURL
	}
	return TierLimitHTTPError{
		Code:       "tier_limit_exceeded",
		Message:    e.Error(),
		Limit:      e.Limit,
		Current:    e.Current,
		Allowed:    e.Allowed,
		UpgradeURL: upgradeURL,
	}
}

// checkNumericLimit returns nil if usage is allowed, or a TierLimitError if
// it exceeds the cap. BUG-016: an allowed value of 0 now denies any positive
// usage — Unlimited is the sole "no cap" sentinel. Plans MUST opt into
// unlimited via the Unlimited constant; an accidental zero is a deny.
func checkNumericLimit(limit string, current, allowed int64) *TierLimitError {
	if allowed == Unlimited || current <= allowed {
		return nil
	}
	return newTierLimitError(limit, current, allowed)
}

func newTierLimitError(limit string, current, allowed int64) *TierLimitError {
	return &TierLimitError{
		Limit:      limit,
		Current:    current,
		Allowed:    allowed,
		UpgradeURL: DefaultUpgradeURL,
	}
}

func effectiveLimit(primary int64, extras map[string]int64, keys ...string) int64 {
	if primary != 0 {
		return primary
	}
	for _, key := range keys {
		if value, ok := extras[normalizeName(key)]; ok {
			return value
		}
	}
	return 0
}
