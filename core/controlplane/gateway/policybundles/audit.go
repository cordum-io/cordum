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
	if identitySource := strings.TrimSpace(entry.IdentitySource); identitySource != "" {
		extra["identity_source"] = identitySource
	}
	if identityLabel := strings.TrimSpace(entry.IdentityLabel); identityLabel != "" {
		extra["identity_label"] = identityLabel
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

// ActorIdentity resolves a stable audit identity from an auth context.
// Priority: bound principal id, then the stable key id (managed key id or
// "static:<fp>"), then a defense-in-depth SHA-256 fingerprint of the raw key.
// It returns (identity, identity_source, identity_label). The raw API key is
// NEVER returned — only the principal id, the stable key id, the key name, or a
// truncated fingerprint.
//   - identity_source taxonomy: "principal" | "api_key:<id>" | "api_key_fp".
//   - identity stays the STABLE id; the human-readable key name (when set)
//     rides in identity_label so audit readers see "ci" alongside id "mk_x".
func ActorIdentity(ac *auth.AuthContext) (identity, source, label string) {
	if ac == nil {
		return "", "", ""
	}
	if pid := strings.TrimSpace(ac.PrincipalID); pid != "" {
		return pid, "principal", ""
	}
	if keyID := strings.TrimSpace(ac.KeyID); keyID != "" {
		return keyID, "api_key:" + keyID, strings.TrimSpace(ac.KeyName)
	}
	if apiKey := strings.TrimSpace(ac.APIKey); apiKey != "" {
		return auth.APIKeyFingerprint(apiKey), "api_key_fp", ""
	}
	return "", "", ""
}

// PolicyActorIdentity resolves (identity, identity_source, identity_label) from
// the request's auth context. See ActorIdentity for the resolution priority.
func PolicyActorIdentity(r *http.Request) (identity, source, label string) {
	if r == nil {
		return "", "", ""
	}
	return ActorIdentity(auth.FromRequest(r))
}

// PolicyActorID extracts the stable audit identity from the request's auth
// context. It delegates to ActorIdentity so existing call sites automatically
// record a non-empty identity for key-only actors, not just bound principals.
func PolicyActorID(r *http.Request) string {
	id, _, _ := PolicyActorIdentity(r)
	return id
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
