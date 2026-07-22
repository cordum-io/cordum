package resource

import "errors"

var (
	// ErrInvalidReference means required metadata or trusted scope is invalid.
	ErrInvalidReference = errors.New("resource: invalid reference")
	// ErrUnknownResolver means no operator-installed resolver matches the ID.
	ErrUnknownResolver = errors.New("resource: unknown resolver")
	// ErrInvalidResolverConfig means an installed resolver policy is unsafe.
	ErrInvalidResolverConfig = errors.New("resource: invalid resolver configuration")
	// ErrExpired means content was expired before or during resolution.
	ErrExpired = errors.New("resource: reference expired")
	// ErrPolicyViolation means the URI falls outside the installed allowlist.
	ErrPolicyViolation = errors.New("resource: resolver policy violation")
	// ErrUnavailable means the locally configured resolver could not return data.
	ErrUnavailable = errors.New("resource: resolver unavailable")
	// ErrSizeMismatch means declared, permitted, and fetched sizes disagree.
	ErrSizeMismatch = errors.New("resource: size mismatch")
	// ErrMediaTypeMismatch means the declared media type is not allowed.
	ErrMediaTypeMismatch = errors.New("resource: media type mismatch")
	// ErrDigestMismatch means fetched bytes do not match the declared digest.
	ErrDigestMismatch = errors.New("resource: digest mismatch")
)
