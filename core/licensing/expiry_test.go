package licensing

import (
	"errors"
	"testing"
	"time"
)

func TestExpiryPhaseTransitions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	zeroGrace := 0

	tests := []struct {
		name             string
		claims           Claims
		wantState        ExpiryState
		wantDays         int
		wantErr          error
		wantGraceEndsAt  time.Time
		wantBreakGlass   bool
		wantCommunityEnt bool
	}{
		{
			name: "valid well before expiry",
			claims: Claims{
				Plan:      string(PlanEnterprise),
				ExpiresAt: now.Add(60 * 24 * time.Hour).Format(time.RFC3339),
			},
			wantState: ExpiryStateValid,
			wantDays:  60,
		},
		{
			name: "warning at 30 days",
			claims: Claims{
				Plan:      string(PlanEnterprise),
				ExpiresAt: now.Add(30 * 24 * time.Hour).Format(time.RFC3339),
			},
			wantState: ExpiryStateWarning,
			wantDays:  30,
		},
		{
			name: "warning at 14 days",
			claims: Claims{
				Plan:      string(PlanEnterprise),
				ExpiresAt: now.Add(14 * 24 * time.Hour).Format(time.RFC3339),
			},
			wantState: ExpiryStateWarning,
			wantDays:  14,
		},
		{
			name: "warning at 7 days",
			claims: Claims{
				Plan:      string(PlanEnterprise),
				ExpiresAt: now.Add(7 * 24 * time.Hour).Format(time.RFC3339),
			},
			wantState: ExpiryStateWarning,
			wantDays:  7,
		},
		{
			name: "grace with default 14 days when claim sets zero",
			claims: Claims{
				Plan:      string(PlanEnterprise),
				ExpiresAt: now.Add(-24 * time.Hour).Format(time.RFC3339),
				GraceDays: &zeroGrace,
			},
			wantState:       ExpiryStateGrace,
			wantDays:        13,
			wantGraceEndsAt: now.Add(13 * 24 * time.Hour),
		},
		{
			name: "degraded after grace with break glass required when sso was configured",
			claims: Claims{
				Plan:      string(PlanEnterprise),
				ExpiresAt: now.Add(-15 * 24 * time.Hour).Format(time.RFC3339),
				Entitlements: &Entitlements{
					SSO: true,
				},
			},
			wantState:        ExpiryStateDegraded,
			wantErr:          ErrLicenseExpired,
			wantBreakGlass:   true,
			wantCommunityEnt: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, err := ExpiryPhase(tc.claims, now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ExpiryPhase() error = %v, want %v", err, tc.wantErr)
			}
			if status.State != tc.wantState {
				t.Fatalf("ExpiryPhase() state = %q, want %q", status.State, tc.wantState)
			}
			if status.DaysRemaining != tc.wantDays {
				t.Fatalf("ExpiryPhase() days_remaining = %d, want %d", status.DaysRemaining, tc.wantDays)
			}
			if !tc.wantGraceEndsAt.IsZero() && !status.GraceEndsAt.Equal(tc.wantGraceEndsAt) {
				t.Fatalf("ExpiryPhase() grace_ends_at = %s, want %s", status.GraceEndsAt, tc.wantGraceEndsAt)
			}
			if got := BreakGlassAdminRequired(status); got != tc.wantBreakGlass {
				t.Fatalf("BreakGlassAdminRequired() = %v, want %v", got, tc.wantBreakGlass)
			}
			if tc.wantCommunityEnt {
				entitlements := EffectiveEntitlements(status, DefaultEntitlements(PlanEnterprise))
				if got := readNamedIntField(entitlements, "MaxWorkers"); got != 3 {
					t.Fatalf("EffectiveEntitlements().MaxWorkers = %d, want community 3", got)
				}
				if got := readNamedIntField(entitlements, "AuditRetentionDays"); got != 7 {
					t.Fatalf("EffectiveEntitlements().AuditRetentionDays = %d, want community 7", got)
				}
			}
		})
	}
}

func TestValidateWindowGraceReturnsGraceError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	claims := Claims{
		Plan:      string(PlanEnterprise),
		ExpiresAt: now.Add(-48 * time.Hour).Format(time.RFC3339),
	}

	status, grace, err := ValidateWindow(claims, now)
	if err != nil {
		t.Fatalf("ValidateWindow() error = %v, want nil", err)
	}
	if status.State != ExpiryStateGrace {
		t.Fatalf("ValidateWindow() state = %q, want %q", status.State, ExpiryStateGrace)
	}
	if grace == nil {
		t.Fatal("ValidateWindow() grace = nil, want GraceError")
	}
	if !grace.GraceUntil.Equal(status.GraceEndsAt) {
		t.Fatalf("GraceUntil = %s, want %s", grace.GraceUntil, status.GraceEndsAt)
	}
}
