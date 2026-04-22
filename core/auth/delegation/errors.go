package delegation

import "errors"

var (
	ErrMalformed        = errors.New("delegation token malformed")
	ErrExpired          = errors.New("delegation token expired")
	ErrNotYetValid      = errors.New("delegation token not yet valid")
	ErrBadSignature     = errors.New("delegation token signature invalid")
	ErrUnknownKeyId     = errors.New("delegation token key id unknown")
	ErrAudienceMismatch = errors.New("delegation token audience mismatch")
	ErrChainTooDeep     = errors.New("delegation token chain too deep")
	ErrScopeExceeded    = errors.New("delegation token scope exceeded")
	ErrRevoked          = errors.New("delegation token revoked")
	ErrNotFound         = errors.New("delegation token not found")
	ErrCascadeTooDeep   = errors.New("delegation revocation cascade too deep")
)

func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrMalformed):
		return "malformed"
	case errors.Is(err, ErrExpired):
		return "expired"
	case errors.Is(err, ErrNotYetValid):
		return "not_yet_valid"
	case errors.Is(err, ErrBadSignature):
		return "bad_signature"
	case errors.Is(err, ErrUnknownKeyId):
		return "unknown_kid"
	case errors.Is(err, ErrAudienceMismatch):
		return "audience_mismatch"
	case errors.Is(err, ErrChainTooDeep):
		return "chain_too_deep"
	case errors.Is(err, ErrScopeExceeded):
		return "scope_exceeded"
	case errors.Is(err, ErrRevoked):
		return "revoked"
	default:
		return ""
	}
}
