package gateway

import (
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/packs"
)

const (
	errorCodePolicyValidationFailed = "POLICY_VALIDATION_FAILED"
	errorCodePolicyVersionConflict  = "POLICY_VERSION_CONFLICT"
	errorCodePolicySigningRequired  = "POLICY_SIGNING_REQUIRED"
	errorCodePolicyShadowConflict   = "POLICY_SHADOW_CONFLICT"
	errorCodePolicyShadowQuery      = "POLICY_SHADOW_QUERY_INVALID"
	errorCodeVelocityRuleInvalid    = "VELOCITY_RULE_INVALID"
	errorCodeVelocityRuleConflict   = "VELOCITY_RULE_CONFLICT"

	errorCodeEvalDatasetValidationFailed = "EVAL_DATASET_VALIDATION_FAILED"
	errorCodeEvalDatasetVersionConflict  = "EVAL_DATASET_VERSION_CONFLICT"
	errorCodeEvalRunNotRunnable          = "EVAL_RUN_NOT_RUNNABLE"
	errorCodeEvalRunConflict             = "EVAL_RUN_CONFLICT"
	errorCodeEvalExtractionFailed        = "EVAL_EXTRACTION_FAILED"

	errorCodePackInstallInvalid     = "PACK_INSTALL_INVALID"
	errorCodePackAlreadyInstalled   = "PACK_ALREADY_INSTALLED"
	errorCodePackDependencyMissing  = "PACK_DEPENDENCY_MISSING"
	errorCodePackInvalidSignature   = "PACK_INVALID_SIGNATURE"
	errorCodePackMarketplaceInvalid = "PACK_MARKETPLACE_INVALID"
)

func policySigningErrorCode(outcome signerOutcome) string {
	if outcome.Status == http.StatusServiceUnavailable {
		return errorCodePolicySigningRequired
	}
	return errorCodePolicyValidationFailed
}

func packInstallErrorCode(installErr *packs.PackInstallError) string {
	if installErr == nil {
		return errorCodePackInstallInvalid
	}
	if installErr.Status == http.StatusConflict {
		return errorCodePackAlreadyInstalled
	}
	msg := strings.ToLower(installErr.Error())
	switch {
	case strings.Contains(msg, "signature"), strings.Contains(msg, "counter-sig"), strings.Contains(msg, "verification"):
		return errorCodePackInvalidSignature
	case strings.Contains(msg, "dependency"), strings.Contains(msg, "requires"):
		return errorCodePackDependencyMissing
	default:
		return errorCodePackInstallInvalid
	}
}
