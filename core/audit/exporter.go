// Package audit provides SIEM-compatible audit event export for Cordum.
//
// Supported backends: webhook (HTTP POST), syslog (RFC 5424),
// Datadog HTTP intake, and AWS CloudWatch Logs.
package audit

import (
	"context"
	"time"
)

// Event types emitted by the audit subsystem.
const (
	EventSafetyDecision = "safety.decision"
	EventSafetyApproval = "safety.approval"
	EventPolicyChange   = "safety.policy_change"
	EventSafetyViolation = "safety.violation"
	EventSystemAuth     = "system.auth"
)

// Severity levels for SIEM events.
const (
	SeverityCritical = "CRITICAL"
	SeverityHigh     = "HIGH"
	SeverityMedium   = "MEDIUM"
	SeverityLow      = "LOW"
	SeverityInfo     = "INFO"
)

// SIEMEvent is the canonical event schema exported to SIEM systems.
type SIEMEvent struct {
	Timestamp     time.Time         `json:"timestamp"`
	EventType     string            `json:"event_type"`
	Severity      string            `json:"severity"`
	TenantID      string            `json:"tenant_id"`
	AgentID       string            `json:"agent_id,omitempty"`
	JobID         string            `json:"job_id,omitempty"`
	Action        string            `json:"action"`
	Decision      string            `json:"decision,omitempty"`
	MatchedRule   string            `json:"matched_rule,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	RiskTags      []string          `json:"risk_tags,omitempty"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	PolicyVersion string            `json:"policy_version,omitempty"`
	Identity      string            `json:"identity,omitempty"`
	Extra         map[string]string `json:"extra,omitempty"`
}

// Exporter sends batches of SIEM events to an external system.
type Exporter interface {
	Export(ctx context.Context, events []SIEMEvent) error
	Close() error
}
