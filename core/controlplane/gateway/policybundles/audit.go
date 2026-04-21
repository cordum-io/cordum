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
	eventType := ClassifyAuditAction(entry.Action)
	severity := ClassifyAuditSeverity(entry.Action)
	extra := map[string]string{}
	if resourceType := strings.TrimSpace(entry.ResourceType); resourceType != "" {
		extra["resource_type"] = resourceType
	}
	if resourceID := strings.TrimSpace(entry.ResourceID); resourceID != "" {
		extra["resource_id"] = resourceID
	}
	if resourceName := strings.TrimSpace(entry.ResourceName); resourceName != "" {
		extra["resource_name"] = resourceName
		if strings.EqualFold(strings.TrimSpace(entry.ResourceType), "job") {
			extra["topic"] = resourceName
		}
	}
	if role := strings.TrimSpace(entry.Role); role != "" {
		extra["role"] = role
	}
	if authSource := strings.TrimSpace(string(entry.AuthSource)); authSource != "" {
		extra["auth_source"] = authSource
	}
	for key, value := range entry.Extra {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		extra[key] = value
	}
	if len(extra) == 0 {
		extra = nil
	}
	if eventType == audit.EventSafetyDecision {
		severity = safetyDecisionSeverity(strings.TrimSpace(entry.Decision), severity)
	}
	return audit.SIEMEvent{
		Timestamp:     ts,
		EventType:     eventType,
		Severity:      severity,
		TenantID:      tenantID,
		AgentID:       entry.AgentID,
		AgentName:     entry.AgentName,
		AgentRiskTier: entry.AgentRiskTier,
		Action:        entry.Action,
		Decision:      strings.TrimSpace(entry.Decision),
		MatchedRule:   strings.TrimSpace(entry.MatchedRule),
		PolicyVersion: strings.TrimSpace(entry.PolicyVersion),
		Identity:      entry.ActorID,
		Reason:        firstNonEmpty(strings.TrimSpace(entry.Reason), strings.TrimSpace(entry.Message)),
		Extra:         extra,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func safetyDecisionSeverity(decision string, fallback string) string {
	switch strings.TrimSpace(strings.ToLower(decision)) {
	case "deny":
		return audit.SeverityHigh
	case "require_approval", "throttle":
		return audit.SeverityMedium
	case "constrain":
		return audit.SeverityLow
	case "allow":
		return audit.SeverityInfo
	default:
		return fallback
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
