package agentd

import (
	"errors"
	"fmt"
	"strings"

	edgecore "github.com/cordum/cordum/core/edge"
)

func validateEvaluateResponse(resp EvaluateResponse) error {
	switch edgecore.EdgeDecision(strings.ToUpper(strings.TrimSpace(resp.Decision))) {
	case edgecore.DecisionAllow,
		edgecore.DecisionDeny,
		edgecore.DecisionRequireApproval,
		edgecore.DecisionThrottle,
		edgecore.DecisionConstrain,
		edgecore.DecisionRecorded:
		return nil
	default:
		message := sanitizeEvaluateErrorText(resp.ErrorMessage)
		if message == "" {
			message = "Gateway returned an unrecognized evaluate decision"
		}
		return fmt.Errorf("%w: invalid decision %q: %s", ErrEvaluateResponseMalformed, sanitizeEvaluateErrorText(resp.Decision), message)
	}
}

// ClassifyEvaluateError maps transport/decode/validation errors into the
// fail-mode categories consumed by ApplyFailMode. The classifier uses only
// bounded, redacted error text; it must never require callers to parse raw
// Gateway response bodies.
func ClassifyEvaluateError(err error) GatewayErrorCategory {
	if err == nil {
		return GatewayErrorNone
	}
	if errors.Is(err, ErrGatewayTimeout) {
		return GatewayErrorTimeout
	}
	if errors.Is(err, ErrEvaluateResponseMalformed) {
		return GatewayErrorMalformed
	}
	text := strings.ToLower(sanitizeEvaluateErrorText(err.Error()))
	if strings.Contains(text, "policy_unavailable") || strings.Contains(text, "safety_unavailable") {
		return GatewayErrorPolicyUnavailable
	}
	return GatewayErrorUnavailable
}

func sanitizeEvaluateErrorText(value string) string {
	return boundMetadataString(redactSecretLike(strings.TrimSpace(value)))
}
