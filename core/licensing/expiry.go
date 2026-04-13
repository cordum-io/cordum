package licensing

import (
	"fmt"
	"strings"
	"time"
)

const defaultGraceDays = 14

var warningThresholdDays = []int{30, 14, 7}

type ExpiryState string

const (
	ExpiryStateValid    ExpiryState = "valid"
	ExpiryStateWarning  ExpiryState = "warning"
	ExpiryStateGrace    ExpiryState = "grace"
	ExpiryStateDegraded ExpiryState = "degraded"
)

type ExpiryStatus struct {
	State         ExpiryState `json:"state"`
	DaysRemaining int         `json:"days_remaining"`
	ExpiresAt     time.Time   `json:"expires_at,omitempty"`
	GraceEndsAt   time.Time   `json:"grace_ends_at,omitempty"`
	SSOConfigured bool        `json:"sso_configured,omitempty"`
}

func ExpiryPhase(claims Claims, now time.Time) (ExpiryStatus, error) {
	now = now.UTC()
	status := ExpiryStatus{
		State:         ExpiryStateValid,
		SSOConfigured: claims.FeatureEnabled("sso") || claims.FeatureEnabled("saml"),
	}

	if ts := strings.TrimSpace(claims.ExpiresAt); ts != "" {
		expiresAt, err := parseTime(ts)
		if err != nil {
			return status, fmt.Errorf("%w: expires_at: %v", ErrLicenseWindowInvalid, err)
		}
		status.ExpiresAt = expiresAt

		if now.After(expiresAt) {
			graceDays := claims.EffectiveGraceDays()
			status.GraceEndsAt = expiresAt.AddDate(0, 0, graceDays)
			if graceDays > 0 && !now.After(status.GraceEndsAt) {
				status.State = ExpiryStateGrace
				status.DaysRemaining = remainingDays(now, status.GraceEndsAt)
				return status, nil
			}
			status.State = ExpiryStateDegraded
			return status, ErrLicenseExpired
		}

		status.DaysRemaining = remainingDays(now, expiresAt)
		if inWarningWindow(status.DaysRemaining) {
			status.State = ExpiryStateWarning
		}
	}

	return status, nil
}

func ValidateWindow(claims Claims, now time.Time) (ExpiryStatus, *GraceError, error) {
	now = now.UTC()
	if ts := strings.TrimSpace(claims.NotBefore); ts != "" {
		notBefore, err := parseTime(ts)
		if err != nil {
			return ExpiryStatus{State: ExpiryStateValid}, nil, fmt.Errorf("%w: not_before: %v", ErrLicenseWindowInvalid, err)
		}
		if now.Before(notBefore) {
			return ExpiryStatus{State: ExpiryStateValid}, nil, ErrLicenseNotActive
		}
	}

	status, err := ExpiryPhase(claims, now)
	if status.State == ExpiryStateGrace {
		return status, &GraceError{
			ExpiredAt:  status.ExpiresAt,
			GraceUntil: status.GraceEndsAt,
			Now:        now,
		}, nil
	}
	return status, nil, err
}

func EffectiveEntitlements(status ExpiryStatus, current Entitlements) Entitlements {
	if status.State == ExpiryStateDegraded {
		return DefaultEntitlements(PlanCommunity)
	}
	return current
}

func BreakGlassAdminRequired(status ExpiryStatus) bool {
	return status.State == ExpiryStateDegraded && status.SSOConfigured
}

func validateWindow(claims Claims, now time.Time) (ExpiryState, *GraceError, error) {
	status, grace, err := ValidateWindow(claims, now)
	return status.State, grace, err
}

func remainingDays(now, target time.Time) int {
	if !target.After(now) {
		return 0
	}
	const day = 24 * time.Hour
	delta := target.Sub(now)
	return int((delta + day - time.Nanosecond) / day)
}

func inWarningWindow(daysRemaining int) bool {
	if daysRemaining <= 0 {
		return false
	}
	return daysRemaining <= warningThresholdDays[0]
}
