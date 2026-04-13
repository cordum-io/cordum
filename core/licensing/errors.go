package licensing

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrLicenseRequired         = errors.New("license required")
	ErrLicensePayloadMissing   = errors.New("license payload missing")
	ErrLicenseSignatureMissing = errors.New("license signature missing")
	ErrLicenseSignatureInvalid = errors.New("license signature invalid")
	ErrLicensePublicKeyMissing = errors.New("license public key missing")
	ErrLicenseWindowInvalid    = errors.New("license window invalid")
	ErrLicenseNotActive        = errors.New("license not active yet")
	ErrLicenseExpired          = errors.New("license expired")
	ErrInvalidPublicKey        = errors.New("invalid public key")
	ErrTierLimitExceeded       = errors.New("tier limit exceeded")
)

// GraceError captures grace-window metadata after signature verification succeeds.
type GraceError struct {
	ExpiredAt  time.Time
	GraceUntil time.Time
	Now        time.Time
}

func (e *GraceError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("license expired but remains in grace until %s", e.GraceUntil.UTC().Format(time.RFC3339))
}

func (e *GraceError) Unwrap() error { return ErrLicenseExpired }

// TierLimitError reports a tier/entitlement limit violation.
type TierLimitError struct {
	Limit      string
	Allowed    int64
	Current    int64
	Plan       string
	UpgradeURL string
}

func (e *TierLimitError) Error() string {
	if e == nil {
		return ""
	}
	if e.UpgradeURL != "" {
		return fmt.Sprintf("%s exceeds allowed limit (%d > %d); upgrade: %s", e.Limit, e.Current, e.Allowed, e.UpgradeURL)
	}
	return fmt.Sprintf("%s exceeds allowed limit (%d > %d)", e.Limit, e.Current, e.Allowed)
}

func (e *TierLimitError) Unwrap() error { return ErrTierLimitExceeded }
