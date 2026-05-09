package audit

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/policy"
)

const (
	// EnvUnifiedDecisionMode selects legacy/unified/dual policy-decision audit emission.
	EnvUnifiedDecisionMode = "AUDIT_UNIFIED_DECISION_MODE"
	// EventPolicyDecisionV2 carries the unified policy.Decision shape in SIEM form.
	EventPolicyDecisionV2 = "policy.decision.v2"
)

// UnifiedDecisionMode controls transition-window audit decision emission.
type UnifiedDecisionMode string

const (
	UnifiedDecisionModeDual    UnifiedDecisionMode = "dual"
	UnifiedDecisionModeLegacy  UnifiedDecisionMode = "legacy"
	UnifiedDecisionModeUnified UnifiedDecisionMode = "unified"
)

var logInvalidUnifiedDecisionModeOnce sync.Once

// ParseUnifiedDecisionMode accepts dual, legacy, or unified; invalid defaults dual.
func ParseUnifiedDecisionMode(raw string) UnifiedDecisionMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(UnifiedDecisionModeDual):
		return UnifiedDecisionModeDual
	case string(UnifiedDecisionModeLegacy):
		return UnifiedDecisionModeLegacy
	case string(UnifiedDecisionModeUnified):
		return UnifiedDecisionModeUnified
	default:
		return UnifiedDecisionModeDual
	}
}

// UnifiedDecisionModeFromEnv reads AUDIT_UNIFIED_DECISION_MODE.
func UnifiedDecisionModeFromEnv() UnifiedDecisionMode {
	raw := os.Getenv(EnvUnifiedDecisionMode)
	mode := ParseUnifiedDecisionMode(raw)
	if mode == UnifiedDecisionModeDual && strings.TrimSpace(raw) != "" {
		if !isKnownUnifiedDecisionMode(raw) {
			logInvalidUnifiedDecisionModeOnce.Do(func() {
				slog.Warn("invalid audit unified decision mode; defaulting to dual", "raw", raw)
			})
		}
	}
	return mode
}

// DecisionEventsForMode returns deterministic legacy/v2 event(s) for a decision.
func DecisionEventsForMode(
	mode UnifiedDecisionMode,
	legacy SIEMEvent,
	decision policy.Decision,
) ([]SIEMEvent, error) {
	mode = normalizeUnifiedDecisionMode(mode)
	if mode != UnifiedDecisionModeUnified {
		if err := validateLegacyDecisionEvent(legacy); err != nil {
			return nil, err
		}
	}
	if mode != UnifiedDecisionModeLegacy {
		if err := validateUnifiedPolicyDecision(decision); err != nil {
			return nil, err
		}
	}
	return decisionEventsForValidMode(mode, legacy, decision), nil
}

func decisionEventsForValidMode(
	mode UnifiedDecisionMode,
	legacy SIEMEvent,
	decision policy.Decision,
) []SIEMEvent {
	switch mode {
	case UnifiedDecisionModeLegacy:
		return []SIEMEvent{cloneSIEMEvent(legacy)}
	case UnifiedDecisionModeUnified:
		return []SIEMEvent{eventFromUnifiedDecision(legacy, decision)}
	default:
		return []SIEMEvent{cloneSIEMEvent(legacy), eventFromUnifiedDecision(legacy, decision)}
	}
}

func eventFromUnifiedDecision(legacy SIEMEvent, decision policy.Decision) SIEMEvent {
	extra := cloneExtra(legacy.Extra)
	setExtra(extra, "source", decision.Source.String())
	setExtra(extra, "bundle_id", decision.BundleID)
	setExtra(extra, "bundle_version", decision.BundleVersion)
	setExtra(extra, "input_ref", decision.InputRef)
	setExtra(extra, "output_ref", decision.OutputRef)
	setExtra(extra, "audit_hash", decision.AuditHash)
	return SIEMEvent{
		Timestamp:     decisionTimestamp(legacy.Timestamp, decision.Timestamp),
		EventType:     EventPolicyDecisionV2,
		Severity:      legacy.Severity,
		TenantID:      legacy.TenantID,
		AgentID:       legacy.AgentID,
		AgentName:     legacy.AgentName,
		AgentRiskTier: legacy.AgentRiskTier,
		JobID:         legacy.JobID,
		Action:        legacy.Action,
		Decision:      decision.Type.String(),
		MatchedRule:   decision.RuleID,
		Reason:        decisionReason(legacy.Reason, decision.Trace),
		RiskTags:      append([]string{}, legacy.RiskTags...),
		Capabilities:  append([]string{}, legacy.Capabilities...),
		PolicyVersion: firstNonEmpty(decision.BundleVersion, legacy.PolicyVersion),
		Identity:      legacy.Identity,
		Extra:         extra,
	}
}

func validateLegacyDecisionEvent(event SIEMEvent) error {
	if strings.TrimSpace(event.EventType) == "" || strings.TrimSpace(event.TenantID) == "" {
		return fmt.Errorf("legacy event requires event_type and tenant_id")
	}
	return nil
}

func validateUnifiedPolicyDecision(decision policy.Decision) error {
	if strings.TrimSpace(decision.RuleID) == "" || decision.Type == "" || decision.Source == "" {
		return fmt.Errorf("policy decision requires source, type, and rule_id")
	}
	return nil
}

func normalizeUnifiedDecisionMode(mode UnifiedDecisionMode) UnifiedDecisionMode {
	switch mode {
	case UnifiedDecisionModeLegacy, UnifiedDecisionModeUnified:
		return mode
	default:
		return UnifiedDecisionModeDual
	}
}

func isKnownUnifiedDecisionMode(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(UnifiedDecisionModeDual), string(UnifiedDecisionModeLegacy), string(UnifiedDecisionModeUnified):
		return true
	default:
		return false
	}
}

func cloneSIEMEvent(event SIEMEvent) SIEMEvent {
	event.RiskTags = append([]string{}, event.RiskTags...)
	event.Capabilities = append([]string{}, event.Capabilities...)
	event.Extra = cloneExtra(event.Extra)
	return event
}

func cloneExtra(extra map[string]string) map[string]string {
	out := make(map[string]string, len(extra)+6)
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func setExtra(extra map[string]string, key string, value string) {
	if strings.TrimSpace(value) != "" {
		extra[key] = strings.TrimSpace(value)
	}
}

func decisionTimestamp(legacy, unified time.Time) time.Time {
	if !unified.IsZero() {
		return unified.UTC()
	}
	if !legacy.IsZero() {
		return legacy.UTC()
	}
	return time.Now().UTC()
}

func decisionReason(legacy string, trace []policy.TraceStep) string {
	for _, step := range trace {
		if reason := strings.TrimSpace(step.Reason); reason != "" {
			return reason
		}
	}
	return strings.TrimSpace(legacy)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
