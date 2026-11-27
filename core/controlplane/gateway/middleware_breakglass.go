package gateway

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

func (s *server) currentBreakGlassState() licensing.BreakGlassState {
	if s == nil {
		return licensing.BreakGlassStateValid
	}
	info := s.currentLicenseInfo()
	if info == nil {
		return licensing.BreakGlassStateValid
	}
	return licensing.NormalizeBreakGlassState(info.Status)
}

func (s *server) requireLicensePermission(w http.ResponseWriter, r *http.Request, permission string) bool {
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return true
	}

	state := s.currentBreakGlassState()
	if licensing.IsAllowedUnderLicenseState(state, permission) {
		if isBreakGlassState(state) {
			s.recordBreakGlassAllow(w, r, state, permission)
		}
		return true
	}

	s.logBreakGlassDecision(r, state, permission, "deny")
	writeBreakGlassDenied(w, state)
	return false
}

// recordBreakGlassAllow writes the allow record of a break-glass decision: the audit
// event, the decision log line and the allow count. When w is a *statusRecorder, it
// writes them once the request is answered, and only for an answer below 400 or a
// connection upgrade. When w is not a *statusRecorder, it writes them at once.
func (s *server) recordBreakGlassAllow(w http.ResponseWriter, r *http.Request, state licensing.BreakGlassState, permission string) {
	record := func() {
		s.emitBreakGlassAudit(r, state, permission)
		s.logBreakGlassDecision(r, state, permission, "allow")
	}
	rec, ok := w.(*statusRecorder)
	if !ok {
		record()
		return
	}
	rec.whenAnswered(func(status int) {
		if status < http.StatusBadRequest {
			record()
		}
	})
}

func (s *server) emitBreakGlassAudit(r *http.Request, state licensing.BreakGlassState, permission string) {
	if s == nil || s.auditExporter == nil {
		return
	}

	tenantID := strings.TrimSpace(tenantFromRequest(r))
	if tenantID == "" {
		tenantID = strings.TrimSpace(s.tenant)
	}

	identity := "anonymous"
	if authCtx := auth.FromRequest(r); authCtx != nil {
		if principal := strings.TrimSpace(authCtx.PrincipalID); principal != "" {
			identity = principal
		} else if role := strings.TrimSpace(authCtx.Role); role != "" {
			identity = role
		}
	}

	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventLicenseBreakglassActivated,
		Severity:  audit.SeverityHigh,
		TenantID:  tenantID,
		Identity:  identity,
		Action:    audit.EventLicenseBreakglassActivated,
		Reason:    string(state),
		Extra: map[string]string{
			"decision":   "allow",
			"method":     r.Method,
			"path":       r.URL.Path,
			"route":      r.URL.Path,
			"permission": permission,
			"state":      string(state),
		},
	})
}

func (s *server) logBreakGlassDecision(r *http.Request, state licensing.BreakGlassState, permission, decision string) {
	if !isBreakGlassState(state) {
		return
	}
	observeBreakGlassDecision(decision, state)

	identity := "anonymous"
	if authCtx := auth.FromRequest(r); authCtx != nil {
		if principal := strings.TrimSpace(authCtx.PrincipalID); principal != "" {
			identity = principal
		} else if role := strings.TrimSpace(authCtx.Role); role != "" {
			identity = role
		}
	}

	slog.Warn("license break-glass decision",
		"decision", decision,
		"state", string(state),
		"permission", permission,
		"method", r.Method,
		"route", r.URL.Path,
		"path", r.URL.Path,
		"tenant", tenantFromRequest(r),
		"principal", identity,
	)
}

func isBreakGlassState(state licensing.BreakGlassState) bool {
	return state == licensing.BreakGlassStateGrace ||
		state == licensing.BreakGlassStateDegraded ||
		state == licensing.BreakGlassStateInvalid
}

func writeBreakGlassDenied(w http.ResponseWriter, state licensing.BreakGlassState) {
	errorCode := "license.degraded"
	message := "license degraded; only break-glass recovery routes remain available"
	if state == licensing.BreakGlassStateGrace {
		errorCode = "license.grace_restricted"
		message = "license grace mode is read-only for this operation"
	}
	if state == licensing.BreakGlassStateInvalid {
		message = "license invalid; only break-glass recovery routes remain available"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	writeJSON(w, map[string]any{
		"error":       message,
		"code":        errorCode,
		"error_code":  errorCode,
		"status":      http.StatusServiceUnavailable,
		"state":       string(state),
		"upgrade_url": licensing.DefaultUpgradeURL,
	})
}
