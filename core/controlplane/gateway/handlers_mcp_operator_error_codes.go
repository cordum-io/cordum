package gateway

import (
	"errors"

	"github.com/cordum/cordum/core/auth/delegation"
)

const (
	errorCodeMCPRangeInvalid             = "MCP_RANGE_INVALID"
	errorCodeMCPSignatureStatusInvalid   = "MCP_SIGNATURE_STATUS_INVALID"
	errorCodeMCPLimitInvalid             = "MCP_LIMIT_INVALID"
	errorCodeMCPAgentIDRequired          = "MCP_AGENT_ID_REQUIRED"
	errorCodeMCPAgentIdentityNotFound    = "MCP_AGENT_IDENTITY_NOT_FOUND"
	errorCodeMCPVerifyRequestInvalid     = "MCP_VERIFY_REQUEST_INVALID"
	errorCodeMCPVerifierUnavailable      = "MCP_VERIFIER_UNAVAILABLE"
	errorCodeMCPHTTPTransportUnavailable = "MCP_HTTP_TRANSPORT_UNAVAILABLE"

	errorCodeDelegationRequestInvalid = "DELEGATION_REQUEST_INVALID"
	errorCodeDelegationRateLimited    = "DELEGATION_RATE_LIMITED"
	errorCodeDelegationScopeExceeded  = "DELEGATION_SCOPE_EXCEEDED"
	errorCodeDelegationChainTooDeep   = "DELEGATION_CHAIN_TOO_DEEP"
	errorCodeDelegationTokenNotFound  = "DELEGATION_TOKEN_NOT_FOUND"
	errorCodeDelegationCascadeTooDeep = "DELEGATION_CASCADE_TOO_DEEP"
	errorCodeDelegationAgentNotFound  = "DELEGATION_AGENT_NOT_FOUND"
)

func delegationIssueErrorCode(err error) string {
	switch {
	case errors.Is(err, delegation.ErrScopeExceeded):
		return errorCodeDelegationScopeExceeded
	case errors.Is(err, delegation.ErrChainTooDeep):
		return errorCodeDelegationChainTooDeep
	case delegation.ErrorCode(err) != "":
		return errorCodeDelegationRequestInvalid
	default:
		return errorCodeDelegationRequestInvalid
	}
}
