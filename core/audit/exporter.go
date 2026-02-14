// Package audit provides SIEM-compatible audit event export for Cordum.
//
// Supported backends: webhook (HTTP POST), syslog (RFC 5424),
// Datadog HTTP intake, and AWS CloudWatch Logs.
package audit

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
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

// NewExporterFromEnv reads CORDUM_AUDIT_EXPORT_* environment variables and
// returns a BufferedExporter wrapping the configured backend.
// Returns nil (no error) if export is disabled (type "none" or empty).
func NewExporterFromEnv() (*BufferedExporter, error) {
	typ := strings.ToLower(os.Getenv("CORDUM_AUDIT_EXPORT_TYPE"))
	if typ == "" || typ == "none" {
		return nil, nil
	}

	var exp Exporter
	var err error

	switch typ {
	case "webhook":
		url := os.Getenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL")
		if url == "" {
			return nil, fmt.Errorf("audit config: CORDUM_AUDIT_EXPORT_WEBHOOK_URL required for webhook export")
		}
		var opts []WebhookOption
		if secret := os.Getenv("CORDUM_AUDIT_EXPORT_WEBHOOK_SECRET"); secret != "" {
			opts = append(opts, WithWebhookSecret(secret))
		}
		exp = NewWebhookExporter(url, opts...)

	case "syslog":
		addr := os.Getenv("CORDUM_AUDIT_EXPORT_SYSLOG_ADDR")
		if addr == "" {
			return nil, fmt.Errorf("audit config: CORDUM_AUDIT_EXPORT_SYSLOG_ADDR required for syslog export (e.g. tcp://host:514)")
		}
		network, address, parseErr := parseSyslogAddr(addr)
		if parseErr != nil {
			return nil, parseErr
		}
		exp, err = NewSyslogExporter(network, address)
		if err != nil {
			return nil, err
		}

	case "datadog":
		apiKey := os.Getenv("CORDUM_AUDIT_EXPORT_DD_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("audit config: CORDUM_AUDIT_EXPORT_DD_API_KEY required for datadog export")
		}
		var opts []DatadogOption
		if site := os.Getenv("CORDUM_AUDIT_EXPORT_DD_SITE"); site != "" {
			opts = append(opts, WithDatadogSite(site))
		}
		if tags := os.Getenv("CORDUM_AUDIT_EXPORT_DD_TAGS"); tags != "" {
			opts = append(opts, WithDatadogTags(tags))
		}
		exp = NewDatadogExporter(apiKey, opts...)

	case "cloudwatch":
		logGroup := os.Getenv("CORDUM_AUDIT_EXPORT_CW_LOG_GROUP")
		logStream := os.Getenv("CORDUM_AUDIT_EXPORT_CW_LOG_STREAM")
		if logGroup == "" || logStream == "" {
			return nil, fmt.Errorf("audit config: CORDUM_AUDIT_EXPORT_CW_LOG_GROUP and CORDUM_AUDIT_EXPORT_CW_LOG_STREAM required")
		}
		exp, err = NewCloudWatchExporter(logGroup, logStream)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("audit config: unknown export type %q (expected webhook|syslog|datadog|cloudwatch|none)", typ)
	}

	slog.Info("audit SIEM export enabled", "type", typ) // #nosec -- value is validated against a fixed allowlist.
	return NewBufferedExporter(exp), nil
}

// parseSyslogAddr parses "tcp://host:port" or "udp://host:port".
func parseSyslogAddr(addr string) (network, address string, err error) {
	for _, proto := range []string{"tcp://", "udp://"} {
		if strings.HasPrefix(addr, proto) {
			return strings.TrimSuffix(proto, "://"), strings.TrimPrefix(addr, proto), nil
		}
	}
	return "", "", fmt.Errorf("audit config: syslog address must start with tcp:// or udp:// (got %q)", addr)
}
