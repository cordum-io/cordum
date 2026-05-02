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
const (
	EventSafetyDecision = "safety.decision"
	// EventDelegationLineage captures the full human-readable delegation chain
	// once per (job_id, token_jti) at dispatch so downstream SIEM rules can
	// correlate the compact safety.decision delegation fields back to the
	// complete issuer ancestry.
	EventDelegationLineage = "delegation.lineage"
	// EventDelegationRejected captures submit-time delegation verification
	// failures before a job is accepted. Reason carries the delegation error
	// code (for example malformed, revoked, or audience_mismatch).
	EventDelegationRejected = "delegation.rejected"
	// EventDelegationRevokedBeforeDispatch captures dispatch-time delegation
	// re-verification failures after a submit already succeeded. Reason carries
	// the stable failure code persisted onto the job / DLQ record.
	EventDelegationRevokedBeforeDispatch = "delegation.revoked_before_dispatch"
	EventSafetyApproval                  = "safety.approval"
	EventPolicyChange                    = "safety.policy_change"
	EventSafetyViolation                 = "safety.violation"
	EventSystemAuth                      = "system.auth"
	// EventMCPToolApproval is emitted for every MCP per-tool approval
	// lifecycle transition: enqueue, approve, reject, expire, consume.
	// The Extra map carries tool_name, args_hash, approval_id,
	// requester, resolver, and outcome so downstream SIEM rules can
	// correlate per-tool activity without parsing natural-language
	// Reason fields.
	EventMCPToolApproval = "mcp.tool_approval"
	// EventMCPToolDenied is emitted when the scope filter rejects an
	// MCP tools/call request. Extra carries tool_name, sub_reason,
	// agent_id, and tenant so downstream SIEM rules can detect
	// privilege-escalation probes.
	EventMCPToolDenied = "mcp.tool_denied"
	// EventMCPToolInvocation is emitted for every completed tools/call
	// regardless of outcome. Extra carries tool_name, agent_id, tenant,
	// duration_ms, and result_size so downstream SIEM rules can correlate
	// activity volume without parsing natural-language Reason fields.
	EventMCPToolInvocation = "mcp.tool_invocation"
	// EventMCPToolOutboundInvocation is emitted when Cordum acts as an
	// MCP client (outbound) — e.g. brokering a remote tool call via the
	// MCP bridge. Extra carries server, tool_name, tenant, duration_ms,
	// and outcome for SIEM correlation of egress activity.
	EventMCPToolOutboundInvocation = "mcp.tool_outbound_invocation"
	EventMCPSignatureInvalid       = "mcp.signature_invalid"
	// EventHeartbeatDisagreement is emitted while heartbeat demotion is in
	// warn mode and session-token authority disagrees with legacy heartbeat
	// recency for the same worker liveness decision.
	EventHeartbeatDisagreement = "heartbeat_disagreement"
	// EventApprovalRevisionMismatch is emitted when the scheduler's
	// approval fast-path rejects a job because the approval_snapshot
	// label does not match the stored SafetyDecisionRecord.PolicySnapshot.
	// Signals that policy drifted between approval time and dispatch time
	// and the fast-path refused to short-circuit on stale constraints.
	EventApprovalRevisionMismatch = "approval.revision_mismatch"
	EventWorkerTrustChange        = "worker_trust_change"
	EventTopicRegistered          = "topic_registered"
	EventTopicUnregistered        = "topic_unregistered"
	// EventLicenseLegacyRejected is emitted when the licensing layer
	// rejects a pre-GA top-level features/limits envelope instead of
	// silently migrating it to the current schema.
	EventLicenseLegacyRejected = "license.legacy_format_rejected"
	// EventLicenseBreakglassActivated is emitted whenever an expired or
	// invalid license still admits a request through one of the explicit
	// break-glass recovery paths.
	EventLicenseBreakglassActivated = "license.breakglass_activated"
	// EventShadowEval is emitted by the Safety Kernel when an active
	// policy evaluation is mirrored against the shadow policy for
	// A/B impact analysis. Extra carries shadow_bundle_id, bundle_id,
	// active_verdict, shadow_verdict, diff, and latency_ms.
	EventShadowEval = "shadow_eval"
	// EventAuthAPIKeyCreated is emitted when an admin creates a new API
	// key via POST /api/v1/auth/keys. Extra carries key_id, key_name,
	// scopes, expires_at, and tenant. Identity carries the actor (the
	// admin who created the key). Pairs with the internal audit-chain
	// entry for forensic correlation; SIEM rules can flag mass-creation
	// or out-of-hours key minting.
	EventAuthAPIKeyCreated = "auth.api_key_created" // #nosec G101 -- audit event name, not a credential.
	// EventAuthAPIKeyRevoked is emitted when a key is revoked via
	// DELETE /api/v1/auth/keys/{id}. Extra carries key_id and tenant.
	// Identity carries the actor who revoked it. SeverityHigh because
	// revocation typically follows a compromise or offboarding event.
	EventAuthAPIKeyRevoked = "auth.api_key_revoked" // #nosec G101 -- audit event name, not a credential.
	// EventAuthRoleUpserted is emitted when a custom RBAC role
	// definition is created or updated via PUT /api/v1/auth/roles/{name}.
	// Extra carries role_name, permissions, inherits, operation
	// (create|update), and tenant. Identity carries the actor. SIEM rules
	// can flag privilege expansion (operator → admin perm grants).
	EventAuthRoleUpserted = "auth.role_upserted"
	// EventAuthRoleDeleted is emitted when a custom RBAC role definition
	// is removed via DELETE /api/v1/auth/roles/{name}. Extra carries
	// role_name and tenant. Identity carries the actor. SIEM rules can
	// detect cleanup-after-attack patterns (delete role to remove evidence).
	EventAuthRoleDeleted = "auth.role_deleted"

	// EDGE-014 audit event constants. These cover the Edge action
	// lifecycle (sessions, executions, decisions, approvals, degraded /
	// fail-closed outcomes, evidence export). Extra map convention:
	// only safe IDs (tenant, session, execution, event), bounded
	// hashes (input_hash, action_hash, policy_snapshot), bounded
	// labels/codes (decision, fail_mode, reason_code, error_code,
	// artifact_type, redaction_level), and approval_ref. Raw command/
	// prompt/tool_response/secret content MUST NEVER appear in Extra.
	EventEdgeSessionStarted    = "edge.session_started"
	EventEdgeSessionEnded      = "edge.session_ended"
	EventEdgeExecutionStarted  = "edge.execution_started"
	EventEdgeExecutionEnded    = "edge.execution_ended"
	EventEdgeActionAttempted   = "edge.action_attempted"
	EventEdgePolicyDecision    = "edge.policy_decision"
	EventEdgeActionDenied      = "edge.action_denied"
	EventEdgeApprovalRequested = "edge.approval_requested"
	EventEdgeApprovalResolved  = "edge.approval_resolved"
	EventEdgeApprovalRejected  = "edge.approval_rejected"
	EventEdgeApprovalExpired   = "edge.approval_expired"
	EventEdgeArtifactExported  = "edge.artifact_exported"
	EventEdgeAgentdDegraded    = "edge.agentd_degraded"
	EventEdgeFailClosed        = "edge.fail_closed"
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
	// HMAC is an optional HMAC-SHA256 of the canonical event payload, keyed
	// with CORDUM_AUDIT_HMAC_KEY. When present it proves the event was
	// produced by a process holding the signing key — SHA-256 hash chaining
	// alone proves ordering and detects external tampering, but cannot
	// distinguish legitimate appends from an attacker who has Redis write
	// access. HMAC closes that gap. Hex-encoded, 64 chars when present.
	HMAC string `json:"hmac,omitempty"`
}

// Exporter sends batches of SIEM events to an external system.
type Exporter interface {
	Export(ctx context.Context, events []SIEMEvent) error
	Close() error
}

// NewExporterFromEnv reads CORDUM_AUDIT_EXPORT_* environment variables and
// returns a BufferedExporter wrapping the configured backend.
// Empty/"none" env values install a discard backend so the audit chain still
// runs even when no streaming SIEM destination is configured.
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
	typ := strings.ToLower(strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_TYPE")))
	if !isDiscardExportType(typ) && !siemExportEnabled(currentEntitlements(resolver)) {
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
	typ := strings.ToLower(strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_TYPE")))
	discardMode := isDiscardExportType(typ)

	var exp Exporter
	var err error

	switch typ {
	case "", "none":
		// No external SIEM backend. The audit chain is now instantiated
		// unconditionally at gateway boot, so returning a nil exporter is
		// safe: the chain still records every event and /api/v1/audit/verify
		// stays healthy. Operators who want audit-export metrics to match a
		// real backend can set CORDUM_AUDIT_EXPORT_TYPE to null|discard|chain-only
		// instead, handled in the next case.
		return nil, nil

	case "null", "discard", "chain-only":
		// Explicit no-op exporter. Behaves like NewDiscardExporter so
		// audit-export counters reflect "a backend exists" even though
		// events are not forwarded anywhere external.
		exp = NewDiscardExporter()
		typ = "null"

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
		return nil, fmt.Errorf("audit config: unknown export type %q (expected webhook|syslog|datadog|cloudwatch|null|discard|chain-only|none)", typ)
	}

	if discardMode {
		slog.Info("audit SIEM export disabled; chain-only mode active", "type", typ) // #nosec -- value is validated against a fixed allowlist.
	} else {
		slog.Info("audit SIEM export enabled", "type", typ) // #nosec -- value is validated against a fixed allowlist.
	}
	return exp, nil
}

func isDiscardExportType(typ string) bool {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "", "none", "null", "discard", "chain-only":
		return true
	default:
		return false
	}
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
