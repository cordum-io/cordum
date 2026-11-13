package licensing

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestLicenseStateDerivation(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, status string
		days         int
		plan         Plan
		gate         BreakGlassState
	}{
		{"valid", "active", 90, PlanEnterprise, BreakGlassStateValid},
		{"warning", "warning", 10, PlanEnterprise, BreakGlassStateValid},
		{"grace", "grace", -3, PlanEnterprise, BreakGlassStateGrace},
		{"degraded", "fallback", -30, PlanCommunity, BreakGlassStateInvalid},
		{"invalid/signature", "fallback", 90, PlanCommunity, BreakGlassStateInvalid},
		{"invalid/not_before", "fallback", 90, PlanCommunity, BreakGlassStateInvalid},
		{"invalid/load", "fallback", 90, PlanCommunity, BreakGlassStateInvalid},
		{"missing", "active", 90, PlanCommunity, BreakGlassStateValid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := buildTestLicense(t, PlanEnterprise, func(e *Entitlements) { e.TelemetryMode = "test" }).Payload
			claims.ExpiresAt = now.AddDate(0, 0, tc.days).Format(time.RFC3339)
			if tc.name == "invalid/not_before" {
				claims.NotBefore = now.AddDate(0, 0, 1).Format(time.RFC3339)
			}
			r := licenseStateResolver(t, claims, &now, tc.name == "invalid/signature")
			switch tc.name {
			case "missing":
				r.loadFromEnv = func() (*License, error) { return nil, nil }
			case "invalid/load":
				r.loadFromEnv = func() (*License, error) { return nil, errors.New("test load failure") }
			case "grace", "degraded":
				checkLicenseExpiryWindow(t, claims, now, tc.name)
			}
			r.Init()
			want := DefaultEntitlements(tc.plan)
			if tc.plan == PlanEnterprise {
				want.TelemetryMode = "test"
			}
			assertLicenseSnapshot(t, r, tc.status, tc.plan, want, tc.gate)
		})
	}
}

func licenseStateResolver(t *testing.T, claims Claims, clock *time.Time, wrongKey bool) *EntitlementResolver {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	license := signLicense(t, claims, priv)
	if wrongKey {
		pub, _, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := NewEntitlementResolver()
	r.loadFromEnv = func() (*License, error) { return license, nil }
	r.publicKeyFromEnv = func() (ed25519.PublicKey, error) { return pub, nil }
	r.now = func() time.Time { return *clock }
	return r
}

func assertLicenseSnapshot(t *testing.T, r *EntitlementResolver, status string, plan Plan, entitlements Entitlements, gate BreakGlassState) {
	t.Helper()
	if got := r.LicenseInfo().Status; got != status {
		t.Errorf("status = %q, want %q", got, status)
	}
	if got := r.ResolvedPlan(); got != plan {
		t.Errorf("plan = %q, want %q", got, plan)
	}
	if got := r.Entitlements(); !reflect.DeepEqual(got, entitlements) {
		t.Errorf("entitlements = %+v, want %+v", got, entitlements)
	}
	if got := NormalizeBreakGlassState(r.LicenseInfo().Status); got != gate {
		t.Errorf("break-glass = %q, want %q", got, gate)
	}
}

func checkLicenseExpiryWindow(t *testing.T, claims Claims, now time.Time, state string) {
	t.Helper()
	if state == "grace" {
		status, grace, err := ValidateWindow(claims, now)
		if status.State != ExpiryStateGrace || grace == nil || err != nil {
			t.Fatalf("window = %+v, %v, %v; want grace, *GraceError, nil", status, grace, err)
		}
		return
	}
	status, err := ExpiryPhase(claims, now)
	if status.State != ExpiryStateDegraded || !errors.Is(err, ErrLicenseExpired) {
		t.Fatalf("phase = %+v, %v; want degraded, ErrLicenseExpired", status, err)
	}
	if got := EffectiveEntitlements(status, *claims.Entitlements); !reflect.DeepEqual(got, DefaultEntitlements(PlanCommunity)) {
		t.Fatalf("degraded entitlements = %+v, want Community defaults", got)
	}
}

func TestLicenseStateChangesOnlyOnReload(t *testing.T) {
	expires := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now := expires.Add(-time.Hour)
	claims := buildTestLicense(t, PlanEnterprise, nil).Payload
	claims.ExpiresAt = expires.Format(time.RFC3339)
	r := licenseStateResolver(t, claims, &now, false)
	r.Init()
	want := DefaultEntitlements(PlanEnterprise)
	assertLicenseSnapshot(t, r, "warning", PlanEnterprise, want, BreakGlassStateValid)
	now = expires.Add(time.Hour)
	assertLicenseSnapshot(t, r, "warning", PlanEnterprise, want, BreakGlassStateValid)
	if plan, err := r.Reload(); err != nil || plan != PlanEnterprise {
		t.Fatalf("reload = %q, %v; want enterprise, nil", plan, err)
	}
	assertLicenseSnapshot(t, r, "grace", PlanEnterprise, want, BreakGlassStateGrace)
	now = expires.AddDate(0, 0, 15)
	assertLicenseSnapshot(t, r, "grace", PlanEnterprise, want, BreakGlassStateGrace)
	if plan, err := r.Reload(); err != nil || plan != PlanCommunity {
		t.Fatalf("reload = %q, %v; want community, nil", plan, err)
	}
	assertLicenseSnapshot(t, r, "fallback", PlanCommunity, DefaultEntitlements(PlanCommunity), BreakGlassStateInvalid)
}

func TestLicenseBreakGlassMatrix(t *testing.T) {
	states := []BreakGlassState{BreakGlassStateValid, BreakGlassStateGrace, BreakGlassStateDegraded, BreakGlassStateInvalid}
	for _, tc := range []struct {
		permission string
		allowed    [4]bool
	}{
		{BreakGlassPermissionLicenseRotate, [4]bool{true, true, true, true}},
		{BreakGlassPermissionAuthLogin, [4]bool{true, true, true, true}},
		{BreakGlassPermissionAuthSession, [4]bool{true, true, true, true}},
		{BreakGlassPermissionSystemHealth, [4]bool{true, true, true, true}},
		{BreakGlassPermissionAuthPassword, [4]bool{true, true, false, false}},
		{BreakGlassPermissionSystemStatus, [4]bool{true, true, false, false}},
		{BreakGlassPermissionAuditExport, [4]bool{true, true, false, false}},
		{BreakGlassPermissionAuditVerify, [4]bool{true, true, false, false}},
		{BreakGlassPermissionTelemetryExport, [4]bool{true, true, false, false}},
		{BreakGlassPermissionMCPVerify, [4]bool{true, true, false, false}},
		{BreakGlassPermissionPacksVerify, [4]bool{true, true, false, false}},
		{BreakGlassPermissionStreamRead, [4]bool{true, true, false, false}},
		{BreakGlassPermissionAdminLocksRead, [4]bool{true, true, false, false}},
		{BreakGlassPermissionLocksWrite, [4]bool{true, false, false, false}},
		{"jobs.write", [4]bool{true, false, false, false}},
		{"jobs.approve", [4]bool{true, false, false, false}},
		{"jobs.read", [4]bool{true, true, false, false}},
		{"policy.write", [4]bool{true, false, false, false}},
		{"workflows.write", [4]bool{true, false, false, false}},
		{"admin.*", [4]bool{true, false, false, false}},
	} {
		var got [4]bool
		for i, state := range states {
			t.Run(string(state)+"/"+tc.permission, func(t *testing.T) {
				got[i] = IsAllowedUnderLicenseState(state, tc.permission)
				if got[i] != tc.allowed[i] {
					t.Errorf("allow = %t, want %t", got[i], tc.allowed[i])
				}
			})
		}
		if (got[1] && !got[0]) || (got[2] && !got[1]) || got[3] != got[2] {
			t.Errorf("non-monotonic restriction for %s: %v", tc.permission, got)
		}
	}
	for raw, want := range map[string]BreakGlassState{"active": BreakGlassStateValid, "warning": BreakGlassStateValid, "unknown": BreakGlassStateInvalid} {
		if got := NormalizeBreakGlassState(raw); got != want {
			t.Errorf("normalize %q = %q, want %q", raw, got, want)
		}
	}
}
