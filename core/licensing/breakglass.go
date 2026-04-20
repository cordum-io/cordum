package licensing

import "strings"

// BreakGlassState is the normalized operational license state used by gateway
// recovery enforcement. Resolver status strings intentionally collapse
// warning/active into valid so normal traffic continues unchanged until the
// grace window starts.
type BreakGlassState string

const (
	BreakGlassStateValid    BreakGlassState = "valid"
	BreakGlassStateGrace    BreakGlassState = "grace"
	BreakGlassStateDegraded BreakGlassState = "degraded"
	BreakGlassStateInvalid  BreakGlassState = "invalid"
)

const (
	BreakGlassPermissionLicenseRotate   = "license.rotate"
	BreakGlassPermissionAuthLogin       = "auth.login"
	BreakGlassPermissionAuthSession     = "auth.session"
	BreakGlassPermissionAuthPassword    = "auth.password"
	BreakGlassPermissionSystemHealth    = "system.health"
	BreakGlassPermissionSystemStatus    = "system.status"
	BreakGlassPermissionAuditExport     = "audit.export"
	BreakGlassPermissionAuditVerify     = "audit.verify"
	BreakGlassPermissionTelemetryExport = "telemetry.export"
	BreakGlassPermissionMCPVerify       = "mcp.verify"
	BreakGlassPermissionPacksVerify     = "packs.verify"
	BreakGlassPermissionStreamRead      = "stream.read"
	BreakGlassPermissionAdminLocksRead  = "admin.locks.read"
	BreakGlassPermissionLocksWrite      = "locks.write"
)

// NormalizeBreakGlassState maps resolver status strings onto the small,
// auditable break-glass state machine.
func NormalizeBreakGlassState(raw string) BreakGlassState {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "active", "valid", "warning":
		return BreakGlassStateValid
	case "grace":
		return BreakGlassStateGrace
	case "degraded":
		return BreakGlassStateDegraded
	case "fallback", "invalid":
		return BreakGlassStateInvalid
	default:
		return BreakGlassStateInvalid
	}
}

// IsAllowedUnderLicenseState answers whether a post-authz route permission is
// still allowed while the installation is in a degraded license mode.
func IsAllowedUnderLicenseState(state BreakGlassState, permission string) bool {
	permission = strings.TrimSpace(strings.ToLower(permission))
	if permission == "" {
		return true
	}

	switch state {
	case BreakGlassStateValid:
		return true
	case BreakGlassStateGrace:
		if strings.HasPrefix(permission, "auth.") {
			return true
		}
		if strings.HasSuffix(permission, ".read") {
			return true
		}
		switch permission {
		case BreakGlassPermissionLicenseRotate,
			BreakGlassPermissionSystemHealth,
			BreakGlassPermissionSystemStatus,
			BreakGlassPermissionAuditExport,
			BreakGlassPermissionAuditVerify,
			BreakGlassPermissionTelemetryExport,
			BreakGlassPermissionMCPVerify,
			BreakGlassPermissionPacksVerify:
			return true
		default:
			return false
		}
	case BreakGlassStateDegraded, BreakGlassStateInvalid:
		switch permission {
		case BreakGlassPermissionLicenseRotate,
			BreakGlassPermissionAuthLogin,
			BreakGlassPermissionAuthSession,
			BreakGlassPermissionSystemHealth:
			return true
		default:
			return false
		}
	default:
		return false
	}
}
