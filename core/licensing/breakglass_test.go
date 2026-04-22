package licensing

import "testing"

func TestNormalizeBreakGlassState(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want BreakGlassState
	}{
		{name: "empty defaults valid", raw: "", want: BreakGlassStateValid},
		{name: "warning collapses to valid", raw: "warning", want: BreakGlassStateValid},
		{name: "grace preserved", raw: "grace", want: BreakGlassStateGrace},
		{name: "degraded preserved", raw: "degraded", want: BreakGlassStateDegraded},
		{name: "fallback becomes invalid", raw: "fallback", want: BreakGlassStateInvalid},
		{name: "unknown becomes invalid", raw: "mystery", want: BreakGlassStateInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeBreakGlassState(tc.raw); got != tc.want {
				t.Fatalf("NormalizeBreakGlassState(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestIsAllowedUnderLicenseState(t *testing.T) {
	tests := []struct {
		name       string
		state      BreakGlassState
		permission string
		want       bool
	}{
		{name: "valid allows writes", state: BreakGlassStateValid, permission: "jobs.write", want: true},
		{name: "grace allows read", state: BreakGlassStateGrace, permission: "jobs.read", want: true},
		{name: "grace denies write", state: BreakGlassStateGrace, permission: "jobs.write", want: false},
		{name: "grace allows auth password", state: BreakGlassStateGrace, permission: BreakGlassPermissionAuthPassword, want: true},
		{name: "grace allows rotate", state: BreakGlassStateGrace, permission: BreakGlassPermissionLicenseRotate, want: true},
		{name: "grace allows system status", state: BreakGlassStateGrace, permission: BreakGlassPermissionSystemStatus, want: true},
		{name: "grace allows audit export", state: BreakGlassStateGrace, permission: BreakGlassPermissionAuditExport, want: true},
		{name: "degraded allows login", state: BreakGlassStateDegraded, permission: BreakGlassPermissionAuthLogin, want: true},
		{name: "degraded allows session", state: BreakGlassStateDegraded, permission: BreakGlassPermissionAuthSession, want: true},
		{name: "degraded allows rotate", state: BreakGlassStateDegraded, permission: BreakGlassPermissionLicenseRotate, want: true},
		{name: "degraded denies status", state: BreakGlassStateDegraded, permission: BreakGlassPermissionSystemStatus, want: false},
		{name: "degraded denies read", state: BreakGlassStateDegraded, permission: "jobs.read", want: false},
		{name: "invalid matches degraded", state: BreakGlassStateInvalid, permission: BreakGlassPermissionAuthLogin, want: true},
		{name: "invalid denies write", state: BreakGlassStateInvalid, permission: BreakGlassPermissionLocksWrite, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAllowedUnderLicenseState(tc.state, tc.permission); got != tc.want {
				t.Fatalf("IsAllowedUnderLicenseState(%q, %q) = %v, want %v", tc.state, tc.permission, got, tc.want)
			}
		})
	}
}
