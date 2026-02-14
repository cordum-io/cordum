package policybundles

import (
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

// AuditEntryToSIEM converts a PolicyAuditEntry to a SIEM-compatible event.
func AuditEntryToSIEM(entry PolicyAuditEntry, tenantID string) audit.SIEMEvent {
	ts, _ := time.Parse(time.RFC3339, entry.CreatedAt)
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return audit.SIEMEvent{
		Timestamp: ts,
		EventType: ClassifyAuditAction(entry.Action),
		Severity:  ClassifyAuditSeverity(entry.Action),
		TenantID:  tenantID,
		Action:    entry.Action,
		Identity:  entry.ActorID,
		Reason:    entry.Message,
		Extra: map[string]string{
			"resource_type": entry.ResourceType,
			"resource_id":   entry.ResourceID,
			"role":          entry.Role,
			"auth_source":   string(entry.AuthSource),
		},
	}
}

// ClassifyAuditAction maps a policy audit action to a SIEM event type.
func ClassifyAuditAction(action string) string {
	a := strings.ToLower(action)
	switch {
	case strings.Contains(a, "deny") || strings.Contains(a, "block"):
		return audit.EventSafetyViolation
	case strings.Contains(a, "approve") || strings.Contains(a, "reject"):
		return audit.EventSafetyApproval
	case strings.Contains(a, "publish") || strings.Contains(a, "edit") ||
		strings.Contains(a, "delete") || strings.Contains(a, "rollback") ||
		strings.Contains(a, "snapshot"):
		return audit.EventPolicyChange
	case strings.Contains(a, "login") || strings.Contains(a, "key") ||
		strings.Contains(a, "user") || strings.Contains(a, "auth"):
		return audit.EventSystemAuth
	default:
		return audit.EventSafetyDecision
	}
}

// ClassifyAuditSeverity maps an action to a SIEM severity level.
func ClassifyAuditSeverity(action string) string {
	a := strings.ToLower(action)
	switch {
	case strings.Contains(a, "deny") || strings.Contains(a, "block"):
		return audit.SeverityHigh
	case strings.Contains(a, "delete") || strings.Contains(a, "rollback"):
		return audit.SeverityMedium
	case strings.Contains(a, "approve") || strings.Contains(a, "reject"):
		return audit.SeverityMedium
	case strings.Contains(a, "publish") || strings.Contains(a, "edit"):
		return audit.SeverityLow
	default:
		return audit.SeverityInfo
	}
}

// PolicyActorID extracts the principal ID from the request's auth context.
func PolicyActorID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if a := auth.FromRequest(r); a != nil && a.PrincipalID != "" {
		return a.PrincipalID
	}
	return ""
}

// PolicyRole extracts and normalizes the role from the request's auth context.
func PolicyRole(r *http.Request) string {
	if r == nil {
		return ""
	}
	if a := auth.FromRequest(r); a != nil && a.Role != "" {
		return auth.NormalizeRole(a.Role)
	}
	return ""
}
