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

	"github.com/cordum/cordum/core/licensing"
)

// Event types emitted by the audit subsystem.
//
// EventShadowEval is the Phase-2 dual-evaluation signal. When a tenant
// has an active shadow policy, every PolicyCheckRequest is evaluated
// against BOTH the active bundle and the shadow bundle; the shadow
// outcome is emitted as a shadow_eval event ALONGSIDE (never in place
// of) the regular safety.decision. The event's Extra map carries:
//
//	shadow_bundle_id — stable ID of the shadow bundle the result came from
//	bundle_id        — active bundle ID the shadow is shadowing
//	active_verdict   — the actual decision that was returned to the caller
//	shadow_verdict   — what the shadow policy would have decided
//	diff             — escalated | relaxed | approval_differ | unchanged
//	active_rule_id   — the rule in the active bundle that matched (if any)
//	shadow_rule_id   — the rule in the shadow bundle that matched (if any)
//	latency_ms       — wall-clock cost of the shadow evaluation in ms
//
// TenantID, JobID, and AgentID live on the SIEMEvent top-level fields
// so existing SIEM correlation rules join on them without reading Extra.
const (
	EventSafetyDecision  = "safety.decision"
	EventSafetyApproval  = "safety.approval"
	EventPolicyChange    = "safety.policy_change"
	EventSafetyViolation = "safety.violation"
	EventSystemAuth      = "system.auth"
	EventMCPToolApproval    = "mcp.tool_approval"
	EventMCPToolDenied      = "mcp.tool_denied"
	EventMCPOutboundSigned   = "mcp.outbound_signed"
	EventMCPSignatureInvalid = "mcp.signature_invalid"
	EventShadowEval          = "shadow_eval"
	// Phase-2 boundary-hardening: topic registry change events. Fired on
	// every pack install / uninstall path that registers or removes
	// topic entries. Extra map carries pack_id, topic_name, capability,
	// actor_id so downstream SIEM correlation can reconstruct the full
	// pack-install lifecycle.
	EventTopicRegistered   = "topic_registered"
	EventTopicUnregistered = "topic_unregistered"
	// EventMCPToolInvocation is emitted once per terminal inbound
	// tools/call — success OR handler error. Pair with
	// mcp.tool_approval (pre-call gate) and mcp.tool_denied
	// (pre-call scope rejection) to reconstruct the full lifecycle.
	// Extra fields: tool_name, args_redacted, result_summary,
	// latency_ms, approval_status, decision.
	EventMCPToolInvocation = "mcp.tool_invocation"
	// EventMCPToolOutboundInvocation is the outbound counterpart
	// emitted once per Cordum-initiated call to an external MCP
	// server. Extra adds server_id + direction=outbound.
	EventMCPToolOutboundInvocation = "mcp.tool_outbound_invocation"
	// EventHeartbeatDisagreement fires per dispatch attempt in warn
	// mode when the session-token authority decision disagrees with
	// what the legacy heartbeat-staleness gate would have produced.
	// Extra: worker_id, tenant, jti, direction
	// (session_allows_heartbeat_blocks | session_blocks_heartbeat_allows),
	// session_auth_alive, heartbeat_alive, job_id, topic.
	EventHeartbeatDisagreement = "heartbeat_disagreement"
	// EventWorkerTrustChange records every transition that changes a
	// worker's trust state: session revoke, mode transition, first
	// issue. Extra: worker_id, tenant, from, to, reason, jti.
	EventWorkerTrustChange = "worker_trust_change"
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
//
// Chain fields (Seq, EventHash, PrevHash) are populated by the audit Chainer
// when an event flows through the consumer pipeline. They form a per-tenant
// append-only hash chain so downstream verification can detect tampering:
//
//   - Seq is a monotonic per-tenant sequence number assigned at append time.
//     The first event for a tenant has Seq=1. Gaps or non-monotonic values
//     indicate missing or out-of-order events.
//   - EventHash is SHA-256 of the canonical JSON encoding of the event with
//     Seq and EventHash cleared (PrevHash is included in the hash input so
//     tampering with a predecessor cascades forward). Hex-encoded.
//   - PrevHash is the EventHash of the tenant's previous event, or empty for
//     the genesis event. Hex-encoded.
//
// All three fields are additive JSON properties; SIEM exporters that do not
// understand them pass them through unchanged.
type SIEMEvent struct {
	Timestamp     time.Time         `json:"timestamp"`
	EventType     string            `json:"event_type"`
	Severity      string            `json:"severity"`
	TenantID      string            `json:"tenant_id"`
	AgentID       string            `json:"agent_id,omitempty"`
	AgentName     string            `json:"agent_name,omitempty"`
	AgentRiskTier string            `json:"agent_risk_tier,omitempty"`
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
	Seq           int64             `json:"seq,omitempty"`
	EventHash     string            `json:"event_hash,omitempty"`
	PrevHash      string            `json:"prev_hash,omitempty"`
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
	exp, err := exporterFromEnv()
	if err != nil || exp == nil {
		return nil, err
	}
	return NewBufferedExporter(exp), nil
}

// NewExporterFromEnvWithEntitlements reads CORDUM_AUDIT_EXPORT_* environment
// variables and applies runtime entitlement gates for SIEM export and audit
// retention. Invalid or missing resolvers gracefully fall back to community
// defaults.
func NewExporterFromEnvWithEntitlements(resolver *licensing.EntitlementResolver) (*BufferedExporter, error) {
	typ := strings.ToLower(os.Getenv("CORDUM_AUDIT_EXPORT_TYPE"))
	if typ == "" || typ == "none" {
		return nil, nil
	}
	if !siemExportEnabled(currentEntitlements(resolver)) {
		slog.Warn("audit SIEM export disabled by entitlement",
			"type", typ,
			"plan", resolvedPlan(resolver),
			"upgrade_url", licensing.DefaultUpgradeURL,
		)
		return nil, nil
	}

	exp, err := exporterFromEnv()
	if err != nil || exp == nil {
		return nil, err
	}
	return NewBufferedExporter(exp, WithRetentionTTL(RetentionTTLFromEntitlements(currentEntitlements(resolver)))), nil
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

func exporterFromEnv() (Exporter, error) {
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

	case "null", "discard", "chain-only":
		// Discard destination for the SIEM stream: events are chained into
		// the per-tenant Merkle audit log (so /api/v1/audit/verify + export
		// work end-to-end) but dropped after that. Intended for dev rigs
		// and prod deployments that rely on `cordumctl audit export` to
		// pull chained events on demand rather than a streaming SIEM.
		exp = NewDiscardExporter()

	default:
		return nil, fmt.Errorf("audit config: unknown export type %q (expected webhook|syslog|datadog|cloudwatch|null|none)", typ)
	}

	slog.Info("audit SIEM export enabled", "type", typ) // #nosec -- value is validated against a fixed allowlist.
	return exp, nil
}

// DiscardExporter implements Exporter by dropping every batch. Used when
// CORDUM_AUDIT_EXPORT_TYPE=null|discard|chain-only so the Merkle audit
// chain is still engaged even though no SIEM backend consumes the stream.
type DiscardExporter struct{}

// NewDiscardExporter returns an Exporter that accepts batches and drops them.
func NewDiscardExporter() *DiscardExporter { return &DiscardExporter{} }

// Export always succeeds without forwarding events anywhere.
func (*DiscardExporter) Export(_ context.Context, _ []SIEMEvent) error { return nil }

// Close is a no-op.
func (*DiscardExporter) Close() error { return nil }

func currentEntitlements(resolver *licensing.EntitlementResolver) licensing.Entitlements {
	if resolver != nil {
		return resolver.Entitlements()
	}
	return licensing.DefaultEntitlements(licensing.PlanCommunity)
}

func resolvedPlan(resolver *licensing.EntitlementResolver) licensing.Plan {
	if resolver != nil {
		return resolver.ResolvedPlan()
	}
	return licensing.PlanCommunity
}

func siemExportEnabled(entitlements licensing.Entitlements) bool {
	return entitlements.FeatureEnabled("siem_export") || entitlements.FeatureEnabled("audit_export")
}

// LegalHoldEnabled reports whether legal hold is permitted by the current
// entitlements payload.
func LegalHoldEnabled(entitlements licensing.Entitlements) bool {
	return entitlements.FeatureEnabled("legal_hold")
}

// RetentionTTLFromEntitlements converts the current audit retention entitlement
// into a TTL. A zero duration means unlimited retention.
func RetentionTTLFromEntitlements(entitlements licensing.Entitlements) time.Duration {
	days := entitlements.AuditRetentionDays
	if days == 0 && entitlements.Limits != nil {
		if limit, ok := entitlements.Limits["audit_retention_days"]; ok {
			days = limit
		}
	}
	switch {
	case days == licensing.Unlimited:
		return 0
	case days <= 0:
		return 7 * 24 * time.Hour
	default:
		return time.Duration(days) * 24 * time.Hour
	}
}

// RequireLegalHoldEntitlement returns a tier-limit error when legal hold is not
// enabled for the current plan/entitlements.
func RequireLegalHoldEntitlement(resolver *licensing.EntitlementResolver) error {
	entitlements := currentEntitlements(resolver)
	if LegalHoldEnabled(entitlements) {
		return nil
	}
	return &licensing.TierLimitError{
		Limit:      "legal_hold",
		Allowed:    0,
		Current:    1,
		Plan:       resolvedPlan(resolver).DisplayName(),
		UpgradeURL: licensing.DefaultUpgradeURL,
	}
}
