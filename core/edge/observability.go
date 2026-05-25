// Package edge — observability primitives.
//
// EDGE-014 introduces a small Recorder interface that Edge call sites use to
// emit metrics, structured logs, and audit events without each handler
// having to know the underlying Prometheus/slog/SIEM plumbing. Two
// implementations ship: a no-op recorder used in tests and contexts where
// observability is intentionally disabled, and a Prometheus-backed
// recorder that registers a stable, bounded label set.
//
// Label discipline:
//   - All labels collapse to a small enum (or "unknown"/"other") before
//     emission. Raw command/path/prompt/session_id/execution_id/event_id/
//     approval_ref/full rule_id/signed URL/error string MUST NEVER appear
//     as a label value. Tests in observability_test.go pin this contract.
//   - Severity for audit events follows: info on allow, medium on
//     require_approval, high on deny/reject, critical on enterprise-strict
//     fail-closed.
//
// This file is created by EDGE-014 step-3 with stub no-op behavior so the
// step-3 RED tests can pin the wire contract before step-7 lands the
// Prometheus implementation.

package edge

import (
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/model"
)

// Recorder is the EDGE-014 Edge observability surface. Every Edge call
// site that needs to emit metrics, structured log attributes, or SIEM
// events goes through one of these methods; no Edge handler may call
// prometheus.NewCounterVec directly.
//
// Implementations MUST be safe for concurrent use.
type Recorder interface {
	// Session metrics.
	RecordSessionCreated(tenant, mode, agentProduct string)
	RecordSessionEnded(tenant, mode, status string)
	SetSessionsActive(tenant, mode string, count int)

	// Execution metrics.
	RecordExecutionStarted(tenant, mode, agentProduct string)
	RecordExecutionEnded(tenant, mode, status string)

	// RecordCreateExecutionAborted emits a metric counter when the store-level
	// CreateExecution path refuses to attach a child execution to a parent
	// session that has transitioned to terminal status (or vanished) between
	// the GetSession read and the WATCH commit. EDGE-054 widened the WATCH
	// set to include the parent session key and re-validates inside the TX,
	// so this counter quantifies how often the orphan-prevention path fires.
	// `reason` is a bounded label collapsed via the Normalize* helpers to
	// {"parent_terminal", "parent_missing", "other", "unknown"}.
	RecordCreateExecutionAborted(reason string)

	// Session cleanup / cap metrics.
	ObserveSessionCleanupDuration(duration time.Duration)
	AddSessionCleanupKeysDeleted(count int)
	RecordSessionCleanupDeadline()
	RecordSessionEventCapRejected()
	RecordSessionSwept()

	// Event persistence / redaction metrics.
	RecordEventPersisted(tenant, layer, kind, decision string)
	RecordEventRedacted(outcome string) // applied | skipped | partial | failed
	RecordHookTimeout(phase string)     // request | gateway | kernel | other

	// Action decisions.
	RecordActionDecision(tenant, layer, kind, decision, mode string)
	RecordActionDenied(tenant, layer, kind, reasonCode string)

	// Approval lifecycle.
	RecordApprovalRequested(tenant, layer, kind string)
	RecordApprovalResolved(tenant, layer, kind, outcome string) // approved | rejected | expired | timeout | invalidated
	ObserveApprovalSweepDuration(duration time.Duration)
	AddApprovalSweepExpired(count int)

	// RecordApprovalEnqueueAborted emits a metric counter when EnqueueApproval
	// refuses to enqueue a new approval because of a fail-closed safety guard
	// — currently only the EDGE-058 event-list-too-large path. `reason`
	// collapses to {"event_list_too_large", "other", "unknown"} via the
	// bounded-label helper. Bounded cardinality.
	RecordApprovalEnqueueAborted(reason string)

	// RecordAppendEventsAborted emits a metric counter when AppendEvents
	// refuses to write a batch because the parent edge session or its
	// execution transitioned to a terminal status between the request entry
	// and the WATCH commit. EDGE-055 widened the WATCH set to include the
	// parent session key, and refreshAppendExecutionsInTx surfaces the
	// typed error this counter quantifies. `reason` is bounded via
	// boundedAppendEventsAbortReason to {"parent_session_terminal",
	// "execution_terminal", "other", "unknown"}.
	RecordAppendEventsAborted(reason string)

	// RecordIdempotencyTTLExtended emits a metric counter when the Edge
	// idempotency record's redis TTL is refreshed on a Reserve retry
	// (EDGE-061 long-running flow contract). `state` is bounded via
	// boundedIdempotencyTTLExtendedState to {"pending", "replay", "other",
	// "unknown"}. Bounded cardinality.
	RecordIdempotencyTTLExtended(state string)

	// RecordIdempotencyWindowExpired emits a metric counter when an Edge
	// idempotency operation rejects because the underlying record has
	// passed the max-in-flight cap (EDGE-061: ErrIdempotencyRecordExpired)
	// or the redis TTL has elapsed but the logical event is already
	// persisted (existing ErrIdempotencyWindowExpired at the append
	// surface). `phase` is bounded via boundedIdempotencyWindowExpiredPhase
	// to {"reserve", "complete", "append", "other", "unknown"}.
	RecordIdempotencyWindowExpired(phase string)

	// Runtime ingest replay-window metrics. Implementations MUST NOT expose
	// raw nonce values or raw collector identifiers as labels; tenant and
	// collector are accepted only so implementations can emit bounded presence
	// labels or correlated safe logs.
	RecordRuntimeReplayFirstSeen(tenant, collector string)
	RecordRuntimeReplayReplayed(tenant, collector string)
	RecordRuntimeReplayWindowFull(tenant, collector string)

	// Degraded / fail-closed outcomes.
	RecordDegraded(tenant, mode, component, reasonCode string)
	RecordFailClosed(tenant, mode, reasonCode string)

	// RecordAgentdResponseWriteAborted emits a metric counter when the agentd
	// local-server hook handler observes a write error from the JSON encoder
	// after `http.Server.WriteTimeout` fires (EDGE-059 slow-loris guard).
	// `reason` collapses to {"write_timeout", "write_error", "other",
	// "unknown"} via the bounded-label helper. Bounded cardinality.
	RecordAgentdResponseWriteAborted(reason string)

	// RecordAgentdShutdownForced emits a metric counter when the agentd
	// shutdown sequence had to force-exit a sub-component because its
	// graceful drain timed out (EDGE-063). `reason` is bounded via
	// boundedAgentdShutdownForcedReason to {"http_server_drain",
	// "heartbeat_drain", "other", "unknown"}.
	RecordAgentdShutdownForced(reason string)

	// RecordEdgeExportRequestRejected emits a metric counter when an Edge
	// session-export request is rejected at request-validation time
	// (EDGE-065 max_events upper bound, plus future request-shape
	// rejections). `reason` is bounded via boundedEdgeExportRejectedReason
	// to {"max_events_too_large", "other", "unknown"}.
	RecordEdgeExportRequestRejected(reason string)

	// RecordRedactionFailed emits a metric counter when an Edge redaction
	// call site falls back to the EDGE-071 fail-closed placeholder because
	// the underlying redactor returned an error or the input exceeded
	// MaxRedactionInputBytes. `site` identifies the call site (collapsed
	// via boundedRedactionFailedSite) and `reason` describes the failure
	// mode (collapsed via boundedRedactionFailedReason). Bounded
	// cardinality. The metric is the operational signal that the
	// data-loss-prevention contract fired — operators must investigate
	// any non-zero value because the placeholder represents real data
	// that could not be safely persisted.
	RecordRedactionFailed(site, reason string)

	// Artifact / export observability.
	RecordArtifactExport(tenant, artifactType, result string)

	// Latency observation.
	ObserveHookLatency(tenant, hookEvent, decision string, duration time.Duration)
	ObserveEvaluateLatency(tenant, layer, kind, decision string, duration time.Duration)

	// Cache observability (no-op until EDGE-018 wires it).
	RecordCacheLookup(tenant, layer, kind, result string) // hit | miss | miss_no_eligibility | invalidated

	// Stream observability.
	AddStreamClients(tenant string, delta int)
	RecordStreamEventSent(tenant string)
	RecordStreamDrop(reason string) // marshal_error | client_buffer_full | tenant_filter | stopped
}

// NoopRecorder is the recorder used when no observability is configured.
// It is also the default returned by NewPrometheusRecorder until step-7
// lands the real implementation, so EDGE-014 step-3 tests can pin the
// interface without depending on concrete Prometheus behavior.
type NoopRecorder struct{}

// NewNoopRecorder returns the singleton no-op Recorder.
func NewNoopRecorder() Recorder { return NoopRecorder{} }

func (NoopRecorder) RecordSessionCreated(string, string, string)                 {}
func (NoopRecorder) RecordSessionEnded(string, string, string)                   {}
func (NoopRecorder) SetSessionsActive(string, string, int)                       {}
func (NoopRecorder) RecordExecutionStarted(string, string, string)               {}
func (NoopRecorder) RecordExecutionEnded(string, string, string)                 {}
func (NoopRecorder) RecordCreateExecutionAborted(string)                         {}
func (NoopRecorder) ObserveSessionCleanupDuration(time.Duration)                 {}
func (NoopRecorder) AddSessionCleanupKeysDeleted(int)                            {}
func (NoopRecorder) RecordSessionCleanupDeadline()                               {}
func (NoopRecorder) RecordSessionEventCapRejected()                              {}
func (NoopRecorder) RecordSessionSwept()                                         {}
func (NoopRecorder) RecordEventPersisted(string, string, string, string)         {}
func (NoopRecorder) RecordEventRedacted(string)                                  {}
func (NoopRecorder) RecordHookTimeout(string)                                    {}
func (NoopRecorder) RecordActionDecision(string, string, string, string, string) {}
func (NoopRecorder) RecordActionDenied(string, string, string, string)           {}
func (NoopRecorder) RecordApprovalRequested(string, string, string)              {}
func (NoopRecorder) RecordApprovalResolved(string, string, string, string)       {}
func (NoopRecorder) ObserveApprovalSweepDuration(time.Duration)                  {}
func (NoopRecorder) AddApprovalSweepExpired(int)                                 {}
func (NoopRecorder) RecordApprovalEnqueueAborted(string)                         {}
func (NoopRecorder) RecordAppendEventsAborted(string)                            {}
func (NoopRecorder) RecordIdempotencyTTLExtended(string)                         {}
func (NoopRecorder) RecordIdempotencyWindowExpired(string)                       {}
func (NoopRecorder) RecordRuntimeReplayFirstSeen(string, string)                 {}
func (NoopRecorder) RecordRuntimeReplayReplayed(string, string)                  {}
func (NoopRecorder) RecordRuntimeReplayWindowFull(string, string)                {}
func (NoopRecorder) RecordDegraded(string, string, string, string)               {}
func (NoopRecorder) RecordFailClosed(string, string, string)                     {}
func (NoopRecorder) RecordAgentdResponseWriteAborted(string)                     {}
func (NoopRecorder) RecordAgentdShutdownForced(string)                           {}
func (NoopRecorder) RecordEdgeExportRequestRejected(string)                      {}
func (NoopRecorder) RecordRedactionFailed(string, string)                        {}
func (NoopRecorder) RecordArtifactExport(string, string, string)                 {}
func (NoopRecorder) ObserveHookLatency(string, string, string, time.Duration)    {}
func (NoopRecorder) ObserveEvaluateLatency(string, string, string, string, time.Duration) {
}
func (NoopRecorder) RecordCacheLookup(string, string, string, string) {}
func (NoopRecorder) AddStreamClients(string, int)                     {}
func (NoopRecorder) RecordStreamEventSent(string)                     {}
func (NoopRecorder) RecordStreamDrop(string)                          {}

// Bounded label normalization helpers. NormalizeDecision/NormalizeLayer/
// NormalizeKind/NormalizeOutcome collapse arbitrary strings to a small
// enum so callers never accidentally emit high-cardinality labels. step-7
// uses these inside the Prometheus recorder; tests use them to assert the
// allowlist contract without depending on the recorder implementation.

// allowedDecisions is the bounded set of decision label values. Anything
// else collapses to "other".
var allowedDecisions = map[string]struct{}{
	"allow":            {},
	"deny":             {},
	"require_approval": {},
	"throttle":         {},
	"constrain":        {},
	"degraded":         {},
	"recorded":         {},
}

// NormalizeDecision returns a bounded decision label; arbitrary input
// (uppercase, mixed case, future enum values) collapses to "allow"/"deny"/
// "require_approval"/"throttle"/"constrain"/"degraded"/"recorded" or "other".
func NormalizeDecision(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedDecisions[v]; ok {
		return v
	}
	return "other"
}

var allowedLayers = map[string]struct{}{
	"hook":     {},
	"mcp":      {},
	"llm":      {},
	"runtime":  {},
	"workflow": {},
	"system":   {},
}

func NormalizeLayer(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedLayers[v]; ok {
		return v
	}
	return "other"
}

var allowedKindPrefixes = []string{
	"hook.",
	"session.",
	"execution.",
	"mcp.",
	"llm.",
	"runtime.",
	"approval.",
}

// NormalizeKind keeps the kind label inside the documented prefix space.
// Free-form/raw input collapses to "other".
func NormalizeKind(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	for _, p := range allowedKindPrefixes {
		if hasPrefix(v, p) {
			return v
		}
	}
	return "other"
}

var allowedApprovalOutcomes = map[string]struct{}{
	"approved":    {},
	"rejected":    {},
	"expired":     {},
	"timeout":     {},
	"invalidated": {},
	"consumed":    {},
}

func NormalizeApprovalOutcome(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedApprovalOutcomes[v]; ok {
		return v
	}
	return "other"
}

var allowedStreamDropReasons = map[string]struct{}{
	"marshal_error":      {},
	"client_buffer_full": {},
	"tenant_filter":      {},
	"stopped":            {},
}

func NormalizeStreamDropReason(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedStreamDropReasons[v]; ok {
		return v
	}
	return "other"
}

var allowedRedactionStatuses = map[string]struct{}{
	"applied": {},
	"skipped": {},
	"partial": {},
	"failed":  {},
}

func NormalizeRedactionStatus(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedRedactionStatuses[v]; ok {
		return v
	}
	return "other"
}

// lowerTrim is a tiny helper used by every Normalize* function. Avoids
// pulling strings.ToLower/TrimSpace into every call site.
func lowerTrim(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if len(out) == 0 {
				continue
			}
			break
		}
		if c >= 'A' && c <= 'Z' {
			out = append(out, c+32)
			continue
		}
		out = append(out, c)
	}
	for len(out) > 0 && (out[len(out)-1] == ' ' || out[len(out)-1] == '\t') {
		out = out[:len(out)-1]
	}
	return string(out)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// EventLogAttrs builds the bounded slog.Attr slice the Edge handlers and
// agentd should use when logging an AgentActionEvent. Only safe, bounded
// fields are included:
//
//   - tenant_id, session_id, execution_id, event_id (safe Edge IDs)
//   - layer, kind (normalized)
//   - tool_name (passed through; classifier already bounds untrusted
//     ToolName via classifyHookEvent's switch on lowercased values, but the
//     mapper output uses the verbatim ToolName so we trim+truncate here
//     defensively)
//   - decision (normalized to bounded enum)
//   - input_hash, action_hash (hashes are safe; never log InputRedacted
//     map wholesale because it can carry redacted-but-still-large content)
//   - duration_ms when known
//   - status (normalized) and a bounded reason_code; reason free-text from
//     untrusted sources is NEVER added — callers wanting a free-text
//     reason must redact it themselves and pass via a separate slog.String
//     after EDGE-004 redaction.
//
// Raw command, prompt, file_path, full URLs, request bodies, error
// strings, and Labels/InputRedacted maps MUST NOT be emitted by this
// helper. Tests in observability_test.go pin this contract with synthetic
// secret injection.
func EventLogAttrs(event AgentActionEvent) []slog.Attr {
	attrs := make([]slog.Attr, 0, 12)
	if v := strings.TrimSpace(event.TenantID); v != "" {
		attrs = append(attrs, slog.String("tenant_id", boundedID(v)))
	}
	if v := strings.TrimSpace(event.SessionID); v != "" {
		attrs = append(attrs, slog.String("session_id", boundedID(v)))
	}
	if v := strings.TrimSpace(event.ExecutionID); v != "" {
		attrs = append(attrs, slog.String("execution_id", boundedID(v)))
	}
	if v := strings.TrimSpace(event.EventID); v != "" {
		attrs = append(attrs, slog.String("event_id", boundedID(v)))
	}
	attrs = append(attrs,
		slog.String("layer", NormalizeLayer(string(event.Layer))),
		slog.String("kind", NormalizeKind(string(event.Kind))),
	)
	if v := strings.TrimSpace(event.ToolName); v != "" {
		attrs = append(attrs, slog.String("tool_name", boundedShortString(v, 32)))
	}
	if v := strings.TrimSpace(string(event.Decision)); v != "" {
		attrs = append(attrs, slog.String("decision", NormalizeDecision(v)))
	}
	if v := strings.TrimSpace(string(event.Status)); v != "" {
		attrs = append(attrs, slog.String("status", boundedShortString(v, 32)))
	}
	if v := strings.TrimSpace(event.InputHash); v != "" {
		attrs = append(attrs, slog.String("input_hash", boundedShortString(v, 80)))
	}
	if event.DurationMS > 0 {
		attrs = append(attrs, slog.Int("duration_ms", event.DurationMS))
	}
	return attrs
}

// SessionLogAttrs builds the bounded slog.Attr slice for an EdgeSession.
// Same discipline as EventLogAttrs: only IDs (bounded), normalized status,
// timestamps. No raw repo URLs, no transcript paths, no raw labels.
func SessionLogAttrs(session EdgeSession) []slog.Attr {
	attrs := make([]slog.Attr, 0, 8)
	if v := strings.TrimSpace(session.TenantID); v != "" {
		attrs = append(attrs, slog.String("tenant_id", boundedID(v)))
	}
	if v := strings.TrimSpace(session.SessionID); v != "" {
		attrs = append(attrs, slog.String("session_id", boundedID(v)))
	}
	if v := strings.TrimSpace(string(session.Mode)); v != "" {
		attrs = append(attrs, slog.String("mode", boundedShortString(v, 32)))
	}
	if v := strings.TrimSpace(string(session.Status)); v != "" {
		attrs = append(attrs, slog.String("status", boundedShortString(v, 32)))
	}
	if v := strings.TrimSpace(session.AgentProduct); v != "" {
		attrs = append(attrs, slog.String("agent_product", boundedShortString(v, 32)))
	}
	if !session.StartedAt.IsZero() {
		attrs = append(attrs, slog.Time("started_at", session.StartedAt))
	}
	if session.EndedAt != nil && !session.EndedAt.IsZero() {
		attrs = append(attrs, slog.Time("ended_at", *session.EndedAt))
	}
	return attrs
}

// boundedID returns a length-bounded ID string suitable for log
// emission. Edge IDs are typically 32-64 char tokens — clamp to 80
// to leave room for prefixes like "edge_sess_" without inviting
// log-line bloat from arbitrary input.
func boundedID(value string) string {
	const maxIDLen = 80
	if len(value) <= maxIDLen {
		return value
	}
	return value[:maxIDLen] + "…"
}

// boundedShortString clamps free-form-ish strings to a small length so
// a malicious caller can't blow up logs. The cap is intentionally tight
// (32-64 typical) because these fields are enum-shaped (tool_name,
// status, mode, agent_product) — anything longer is suspicious.
func boundedShortString(value string, max int) string {
	v := strings.TrimSpace(value)
	if max <= 0 {
		max = 32
	}
	if len(v) <= max {
		return v
	}
	return v[:max] + "…"
}

// SIEMEventForAction builds an `audit.SIEMEvent` for an Edge AgentActionEvent.
// The EventType is determined by the event's decision: ALLOW/RECORDED →
// `edge.policy_decision`; DENY → `edge.action_denied`; REQUIRE_APPROVAL →
// `edge.approval_requested`. Severity follows architect's table: allow/info,
// require_approval/medium, deny/reject/high.
//
// Extra carries only safe values: session_id, execution_id, event_id, layer,
// kind, tool_name (bounded), input_hash, action_hash, policy_snapshot,
// rule_id/tier (bounded), approval_ref. Raw InputRedacted/Labels/Reason MUST NOT
// be added by callers via this builder.
func SIEMEventForAction(event AgentActionEvent) audit.SIEMEvent {
	decision := strings.ToUpper(strings.TrimSpace(string(event.Decision)))
	eventType := audit.EventEdgePolicyDecision
	severity := audit.SeverityInfo
	switch decision {
	case "DENY":
		eventType = audit.EventEdgeActionDenied
		severity = audit.SeverityHigh
	case "REQUIRE_APPROVAL":
		eventType = audit.EventEdgeApprovalRequested
		severity = audit.SeverityMedium
	case "THROTTLE":
		eventType = audit.EventEdgeActionDenied
		severity = audit.SeverityMedium
	}
	timestamp := event.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	se := audit.SIEMEvent{
		Timestamp:    timestamp,
		EventType:    eventType,
		Severity:     severity,
		TenantID:     boundedID(event.TenantID),
		AgentName:    boundedShortString(event.AgentName, model.MaxAgentNameLen),
		Action:       boundedShortString(event.ActionName, 64),
		Decision:     NormalizeDecision(string(event.Decision)),
		MatchedRule:  boundedShortString(event.RuleID, 80),
		RiskTags:     boundedTagSlice(event.RiskTags, 8),
		Capabilities: boundedCapabilities(event.Capability),
		Identity:     boundedID(event.PrincipalID),
		Extra:        actionExtra(event),
	}
	// Edge actions are not Cordum Jobs by themselves; SIEMEvent.JobID is
	// only populated when the Edge action is linked to a real production
	// Job/WorkflowRun. AgentActionEvent does not carry a job_id today,
	// so we leave SIEMEvent.JobID empty per ADR-010.
	return se
}

// SIEMEventForSessionStarted builds an audit event for an EdgeSession that
// just transitioned to the active state. Severity is info — session creation
// is benign.
func SIEMEventForSessionStarted(session EdgeSession) audit.SIEMEvent {
	timestamp := session.StartedAt
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	se := audit.SIEMEvent{
		Timestamp: timestamp,
		EventType: audit.EventEdgeSessionStarted,
		Severity:  audit.SeverityInfo,
		TenantID:  boundedID(session.TenantID),
		AgentName: boundedShortString(session.AgentName, model.MaxAgentNameLen),
		Action:    "edge_session_create",
		Identity:  boundedID(session.PrincipalID),
		Extra:     sessionExtra(session),
	}
	return se
}

// SIEMEventForSessionEnded builds an audit event for a session that has
// transitioned to a terminal status. Severity is info on clean end, high
// on failed/degraded.
func SIEMEventForSessionEnded(session EdgeSession) audit.SIEMEvent {
	severity := audit.SeverityInfo
	switch session.Status {
	case SessionStatusFailed, SessionStatusDegraded:
		severity = audit.SeverityHigh
	}
	timestamp := time.Now().UTC()
	if session.EndedAt != nil && !session.EndedAt.IsZero() {
		timestamp = *session.EndedAt
	}
	se := audit.SIEMEvent{
		Timestamp: timestamp,
		EventType: audit.EventEdgeSessionEnded,
		Severity:  severity,
		TenantID:  boundedID(session.TenantID),
		AgentName: boundedShortString(session.AgentName, model.MaxAgentNameLen),
		Action:    "edge_session_end",
		Identity:  boundedID(session.PrincipalID),
		Extra:     sessionExtra(session),
	}
	return se
}

// SIEMEventForApprovalResolved builds an audit event for an approval that
// reached a terminal state (approved/rejected/expired/invalidated/consumed).
// Severity follows: approved/info, rejected/high, expired/medium,
// invalidated/medium.
func SIEMEventForApprovalResolved(tenantID, approvalRef, ruleID, outcome, resolverID string, at time.Time, extra map[string]string) audit.SIEMEvent {
	normalized := NormalizeApprovalOutcome(outcome)
	severity := audit.SeverityInfo
	eventType := audit.EventEdgeApprovalResolved
	switch normalized {
	case "rejected":
		severity = audit.SeverityHigh
		eventType = audit.EventEdgeApprovalRejected
	case "expired":
		severity = audit.SeverityMedium
		eventType = audit.EventEdgeApprovalExpired
	case "invalidated":
		severity = audit.SeverityMedium
	case "timeout":
		severity = audit.SeverityMedium
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	out := audit.SIEMEvent{
		Timestamp:   at,
		EventType:   eventType,
		Severity:    severity,
		TenantID:    boundedID(tenantID),
		Action:      "edge_approval_resolved",
		Decision:    normalized,
		MatchedRule: boundedShortString(ruleID, 80),
		Identity:    boundedID(resolverID),
		Extra:       approvalExtra(approvalRef, extra),
	}
	return out
}

// SIEMEventForFailClosed builds an audit event for an enterprise-strict
// fail-closed outcome (Gateway unavailable, agentd unavailable, etc.).
// Severity is critical — the user's action was blocked because Cordum
// could not produce a fresh governance decision.
func SIEMEventForFailClosed(tenantID, mode, component, reasonCode string, at time.Time) audit.SIEMEvent {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return audit.SIEMEvent{
		Timestamp: at,
		EventType: audit.EventEdgeFailClosed,
		Severity:  audit.SeverityCritical,
		TenantID:  boundedID(tenantID),
		Action:    "edge_fail_closed",
		Decision:  "deny",
		Extra: map[string]string{
			"mode":        boundedMode(mode),
			"component":   boundedComponent(component),
			"reason_code": boundedReasonCode(reasonCode),
		},
	}
}

// SIEMEventForDegraded builds an audit event for a degraded state (Gateway
// timeout, agentd degraded, evidence write failure). Severity is medium
// for observe mode, high for local-dev-enforce.
func SIEMEventForDegraded(tenantID, mode, component, reasonCode string, at time.Time) audit.SIEMEvent {
	severity := audit.SeverityMedium
	if strings.EqualFold(strings.TrimSpace(mode), "local-dev-enforce") {
		severity = audit.SeverityHigh
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return audit.SIEMEvent{
		Timestamp: at,
		EventType: audit.EventEdgeAgentdDegraded,
		Severity:  severity,
		TenantID:  boundedID(tenantID),
		Action:    "edge_agentd_degraded",
		Decision:  "degraded",
		Extra: map[string]string{
			"mode":        boundedMode(mode),
			"component":   boundedComponent(component),
			"reason_code": boundedReasonCode(reasonCode),
		},
	}
}

// SendSIEMEvent forwards the event to the supplied AuditSender, swallowing
// nil-sender / panic and never failing the caller. Edge call sites use this
// to make audit emission strictly best-effort: a missing or failing audit
// pipeline must NEVER change a policy/evaluate/hook decision.
func SendSIEMEvent(sender audit.AuditSender, event audit.SIEMEvent) {
	if sender == nil {
		return
	}
	// Defensive producer-side default: SIEMEvent builders in this
	// package (SIEMEventForAction, SIEMEventForSessionStarted, ...)
	// propagate event.TenantID verbatim from the source AgentActionEvent
	// / EdgeSession; an event that arrived tenantless (anonymous hook
	// bridge, system bootstrap, future producer regression) would land
	// at the sink-level fallback at slog.Warn. Defaulting here keeps
	// the chain populated without per-event log noise. task-3fad45d3.
	if strings.TrimSpace(event.TenantID) == "" {
		event.TenantID = model.DefaultTenant
	}
	defer func() {
		// AuditSender.Send is documented as non-error-returning, but we
		// guard against panics defensively because audit-pipeline outage
		// must not kill the calling request.
		_ = recover()
	}()
	sender.Send(event)
}

// actionTargetLabelAllowlist is the HARD-CODED, ORDERED set of classifier
// descriptor label keys that actionExtra copies from event.Labels into the
// SIEM Extra map under stable, redaction-safe names. This allowlist is the
// ONLY thing read from event.Labels for descriptive targeting; actionExtra
// MUST NEVER range over event.Labels because Labels also carries
// caller-supplied keys (agent.product and arbitrary context labels) whose
// values are not safe to surface in the audit trail. Every value the
// classifier writes under these keys is already a bounded enum or a
// safeLabelValue (secret-redacted + length-bounded) by the time the gateway
// merges classification.Labels onto event.Labels (handlers_edge_evaluate.go
// edgeEvaluateMergeLabels); each copied value is re-bounded via
// boundedShortString defensively. A slice (not a map) keeps the allowlist a
// single grep-able list and structurally forbids an accidental range-over-Labels.
var actionTargetLabelAllowlist = []struct{ src, dst string }{
	{"path.class", "target_class"},
	{"path.sensitive_area", "target_sensitive_area"},
	{"path.traversal", "target_traversal"},
	{"command.class", "command_class"},
	{"command.family", "command_family"},
	{"mcp.server", "mcp_server"},
	{"mcp.tool", "mcp_tool"},
	{"mcp.action", "mcp_action"},
	{"runtime.event", "runtime_event"},
	{"runtime.host", "runtime_host"},
}

// actionExtra builds the safe Extra map for an AgentActionEvent.
func actionExtra(event AgentActionEvent) map[string]string {
	extra := map[string]string{
		"session_id":   boundedID(event.SessionID),
		"execution_id": boundedID(event.ExecutionID),
		"event_id":     boundedID(event.EventID),
		"layer":        NormalizeLayer(string(event.Layer)),
		"kind":         NormalizeKind(string(event.Kind)),
	}
	if v := strings.TrimSpace(event.ToolName); v != "" {
		extra["tool_name"] = boundedShortString(v, 32)
	}
	if v := strings.TrimSpace(event.InputHash); v != "" {
		extra["input_hash"] = boundedShortString(v, 80)
	}
	if v := strings.TrimSpace(event.PolicySnapshot); v != "" {
		extra["policy_snapshot"] = boundedShortString(v, 80)
	}
	if v := strings.TrimSpace(event.RuleTier); v != "" {
		extra["tier"] = boundedRuleTier(v)
	}
	if v := strings.TrimSpace(event.ApprovalRef); v != "" {
		extra["approval_ref"] = boundedShortString(v, 64)
	}
	// Human principal display label (task-c8d4b056). Already sanitized at the
	// session boundary + backfilled onto the action; re-bounded defensively.
	// NOTE: agent attribution rides the structured SIEMEvent.AgentName field —
	// agent_product is deliberately NOT copied into action Extra (it is a
	// non-allowlisted caller label here; see observability_test.go).
	if v := strings.TrimSpace(event.PrincipalDisplayName); v != "" {
		extra["principal_display_name"] = boundedShortString(v, model.MaxAgentNameLen)
	}
	// EDGE governance descriptive targets (task-6a93a0fc): copy ONLY the
	// hard-coded classifier-descriptor allowlist from event.Labels into Extra
	// so an incident responder sees WHAT class of thing was targeted (secret
	// file? destructive shell? MCP mutation?) without ever exposing the raw
	// path/command/prompt. NEVER range over event.Labels (it carries
	// caller-supplied keys); each value is re-bounded via boundedShortString.
	for _, pair := range actionTargetLabelAllowlist {
		if v := strings.TrimSpace(event.Labels[pair.src]); v != "" {
			extra[pair.dst] = boundedShortString(v, 64)
		}
	}
	if summary := actionTargetSummary(event); summary != "" {
		extra["target_summary"] = summary
	}
	// Langfuse-like investigability pointer (task-c8d4b056 step 8): surface the
	// FIRST artifact's content hash as a safe pointer so an investigator can
	// pivot to the redacted evidence bundle. NEVER the content itself — the
	// audit row stays free of raw input/output; only the SHA pointer is copied.
	for _, ptr := range event.ArtifactPointers {
		if sha := strings.TrimSpace(ptr.SHA256); sha != "" {
			extra["artifact_id"] = boundedShortString(sha, 80)
			break
		}
	}
	extra["redaction_status"] = redactionStatusForAction(event)
	return extra
}

// actionTargetSummary composes a single compact, pivotable "class:subclass"
// descriptor for an action from the SAME hard-coded classifier enums that
// actionTargetLabelAllowlist copies — never from raw input. Priority follows
// the most security-relevant axis first: shell command, then file path, then
// MCP call, then runtime event. The result is human-readable AND greppable
// (e.g. "shell:destructive/filesystem_delete", "file:secret",
// "mcp:github/create_issue", "runtime:network.connect/api.example.com").
// Returns "" when the event carries none of the allowlisted descriptors
// (e.g. nil/empty Labels) so the caller omits the key. Bounded defensively.
func actionTargetSummary(event AgentActionEvent) string {
	label := func(key string) string { return strings.TrimSpace(event.Labels[key]) }
	var summary string
	switch {
	case label("command.class") != "":
		summary = "shell:" + label("command.class")
		if family := label("command.family"); family != "" {
			summary += "/" + family
		}
	case label("path.class") != "":
		summary = "file:" + label("path.class")
		if area := label("path.sensitive_area"); area != "" {
			summary += "/" + area
		}
	case label("mcp.server") != "" || label("mcp.tool") != "" || label("mcp.action") != "":
		primary := firstNonEmpty(label("mcp.server"), label("mcp.tool"))
		summary = "mcp:" + primary
		if secondary := firstNonEmpty(label("mcp.action"), label("mcp.tool")); secondary != "" && secondary != primary {
			summary += "/" + secondary
		}
	case label("runtime.event") != "":
		summary = "runtime:" + label("runtime.event")
		if host := label("runtime.host"); host != "" {
			summary += "/" + host
		}
	default:
		return ""
	}
	return boundedShortString(summary, 96)
}

func redactionStatusForAction(event AgentActionEvent) string {
	for _, key := range []string{"redaction_status", "redaction.status"} {
		if event.Labels != nil {
			if status := NormalizeRedactionStatus(event.Labels[key]); status != "unknown" {
				return status
			}
		}
	}
	if len(event.InputRedacted) > 0 || strings.TrimSpace(event.InputHash) != "" {
		return "applied"
	}
	return "skipped"
}

func boundedRuleTier(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "global", "workflow", "job":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "unknown"
	}
}

// sessionExtra builds the safe Extra map for an EdgeSession lifecycle event.
func sessionExtra(session EdgeSession) map[string]string {
	extra := map[string]string{
		"session_id": boundedID(session.SessionID),
		"mode":       boundedShortString(string(session.Mode), 32),
		"status":     boundedShortString(string(session.Status), 32),
	}
	if v := strings.TrimSpace(session.AgentProduct); v != "" {
		extra["agent_product"] = boundedShortString(v, 32)
	}
	// Human principal display label (task-c8d4b056), already sanitized at the
	// session-create boundary; re-bounded defensively. Lets the audit summary's
	// ActorLabel read a friendly name instead of the bare principal_id.
	if v := strings.TrimSpace(session.PrincipalDisplayName); v != "" {
		extra["principal_display_name"] = boundedShortString(v, model.MaxAgentNameLen)
	}
	return extra
}

// approvalExtra builds the safe Extra map for an approval audit event,
// bounding the approval_ref and merging caller-supplied bounded extras.
func approvalExtra(approvalRef string, extra map[string]string) map[string]string {
	out := map[string]string{
		"approval_ref": boundedShortString(approvalRef, 64),
	}
	for k, v := range extra {
		out[boundedShortString(k, 32)] = boundedShortString(v, 80)
	}
	return out
}

// boundedTagSlice returns a copy of tags with each entry trimmed/clamped
// and the slice length bounded; nil-safe and empty-safe.
func boundedTagSlice(tags []string, maxEntries int) []string {
	if len(tags) == 0 {
		return nil
	}
	if maxEntries <= 0 {
		maxEntries = 8
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if len(out) >= maxEntries {
			break
		}
		s := boundedShortString(t, 32)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// boundedCapabilities returns a single-entry slice for the SIEMEvent
// Capabilities field; the Edge classifier emits a single Capability per
// action, so we wrap rather than introducing an array contract.
func boundedCapabilities(capability string) []string {
	v := strings.TrimSpace(capability)
	if v == "" {
		return nil
	}
	return []string{boundedShortString(v, 32)}
}

// SIEMEventForExecutionStarted builds an audit event for an execution
// that just transitioned to running. Severity info — execution start
// is benign.
func SIEMEventForExecutionStarted(exec AgentExecution) audit.SIEMEvent {
	timestamp := exec.StartedAt
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	return audit.SIEMEvent{
		Timestamp: timestamp,
		EventType: audit.EventEdgeExecutionStarted,
		Severity:  audit.SeverityInfo,
		TenantID:  boundedID(exec.TenantID),
		Action:    "edge_execution_start",
		JobID:     boundedID(strings.TrimSpace(exec.JobID)),
		Extra:     executionExtra(exec),
	}
}

// SIEMEventForExecutionEnded builds an audit event for an execution that
// has reached a terminal status. Severity info on succeeded; high on
// failed/timeout/degraded; medium on cancelled.
func SIEMEventForExecutionEnded(exec AgentExecution) audit.SIEMEvent {
	severity := audit.SeverityInfo
	switch exec.Status {
	case ExecutionStatusFailed, ExecutionStatusTimeout, ExecutionStatusDegraded:
		severity = audit.SeverityHigh
	case ExecutionStatusCancelled:
		severity = audit.SeverityMedium
	}
	timestamp := time.Now().UTC()
	if exec.EndedAt != nil && !exec.EndedAt.IsZero() {
		timestamp = *exec.EndedAt
	}
	return audit.SIEMEvent{
		Timestamp: timestamp,
		EventType: audit.EventEdgeExecutionEnded,
		Severity:  severity,
		TenantID:  boundedID(exec.TenantID),
		Action:    "edge_execution_end",
		JobID:     boundedID(strings.TrimSpace(exec.JobID)),
		Extra:     executionExtra(exec),
	}
}

// SIEMEventForArtifactExported builds an audit event for an artifact
// export operation. Severity follows result: ok/info, failed/missing/
// truncated/oversize/medium, unauthorized/tenant_mismatch/high.
//
// Extra carries artifact_type, sha256 (length-bounded), redaction_level,
// retention_class — never the raw URI/query string.
func SIEMEventForArtifactExported(pointer ArtifactPointer, result string) audit.SIEMEvent {
	bounded := boundedResult(result)
	severity := audit.SeverityInfo
	switch bounded {
	case "failed", "missing", "truncated", "oversize":
		severity = audit.SeverityMedium
	case "unauthorized", "tenant_mismatch":
		severity = audit.SeverityHigh
	}
	timestamp := pointer.CreatedAt
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	extra := map[string]string{
		"artifact_type": boundedArtifactType(string(pointer.ArtifactType)),
		"result":        bounded,
		"session_id":    boundedID(pointer.SessionID),
		"execution_id":  boundedID(pointer.ExecutionID),
		"event_id":      boundedID(pointer.EventID),
	}
	if v := strings.TrimSpace(pointer.SHA256); v != "" {
		extra["sha256"] = boundedShortString(v, 80)
	}
	if v := strings.TrimSpace(string(pointer.RedactionLevel)); v != "" {
		extra["redaction_level"] = boundedShortString(v, 32)
	}
	if v := strings.TrimSpace(string(pointer.RetentionClass)); v != "" {
		extra["retention_class"] = boundedShortString(v, 32)
	}
	return audit.SIEMEvent{
		Timestamp: timestamp,
		EventType: audit.EventEdgeArtifactExported,
		Severity:  severity,
		TenantID:  boundedID(pointer.TenantID),
		Action:    "edge_artifact_export",
		Extra:     extra,
	}
}

// SIEMEventForApprovalRequested builds an audit event for an approval
// that was just requested. Severity is medium — the action was held
// pending human review. Extra carries approval_ref/rule_id/policy_snapshot
// and bounded session/execution/event IDs; raw Reason is NEVER promoted.
func SIEMEventForApprovalRequested(apr EdgeApproval) audit.SIEMEvent {
	timestamp := apr.CreatedAt
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	extra := map[string]string{
		"approval_ref": boundedShortString(apr.ApprovalRef, 64),
		"session_id":   boundedID(apr.SessionID),
		"execution_id": boundedID(apr.ExecutionID),
		"event_id":     boundedID(apr.EventID),
	}
	if v := strings.TrimSpace(apr.RuleID); v != "" {
		extra["rule_id"] = boundedShortString(v, 80)
	}
	if v := strings.TrimSpace(apr.PolicySnapshot); v != "" {
		extra["policy_snapshot"] = boundedShortString(v, 80)
	}
	if v := strings.TrimSpace(apr.ActionHash); v != "" {
		extra["action_hash"] = boundedShortString(v, 80)
	}
	if v := strings.TrimSpace(apr.InputHash); v != "" {
		extra["input_hash"] = boundedShortString(v, 80)
	}
	return audit.SIEMEvent{
		Timestamp:   timestamp,
		EventType:   audit.EventEdgeApprovalRequested,
		Severity:    audit.SeverityMedium,
		TenantID:    boundedID(apr.TenantID),
		Action:      "edge_approval_requested",
		Decision:    "require_approval",
		MatchedRule: boundedShortString(apr.RuleID, 80),
		Identity:    boundedID(apr.PrincipalID),
		Extra:       extra,
	}
}

// executionExtra builds the safe Extra map for an AgentExecution
// lifecycle event. Adapter/mode/workflow_run_id/step_id/attempt are
// bounded; Labels are NEVER promoted because they can carry user input.
func executionExtra(exec AgentExecution) map[string]string {
	extra := map[string]string{
		"execution_id": boundedID(exec.ExecutionID),
		"session_id":   boundedID(exec.SessionID),
		"adapter":      boundedShortString(string(exec.Adapter), 32),
		"mode":         boundedShortString(string(exec.Mode), 32),
		"status":       boundedShortString(string(exec.Status), 32),
	}
	if v := strings.TrimSpace(exec.WorkflowRunID); v != "" {
		extra["workflow_run_id"] = boundedID(v)
	}
	if v := strings.TrimSpace(exec.StepID); v != "" {
		extra["step_id"] = boundedShortString(v, 64)
	}
	if exec.Attempt > 0 {
		extra["attempt"] = strconvItoa(exec.Attempt)
	}
	extra["event_counts"] = executionEventCounts(exec.Metrics)
	return extra
}

func executionEventCounts(metrics ExecutionMetrics) string {
	return "events=" + strconv.Itoa(nonNegativeMetric(metrics.Events)) +
		",allow=" + strconv.Itoa(nonNegativeMetric(metrics.Allow)) +
		",deny=" + strconv.Itoa(nonNegativeMetric(metrics.Deny)) +
		",require_approval=" + strconv.Itoa(nonNegativeMetric(metrics.RequireApproval)) +
		",artifacts=" + strconv.Itoa(nonNegativeMetric(metrics.Artifacts))
}

func nonNegativeMetric(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

// strconvItoa is a tiny inline replacement for strconv.Itoa so we don't
// introduce a strconv import for a single call site. Negative inputs
// return "0" because Edge attempt counters are always non-negative.
func strconvItoa(n int) string {
	if n <= 0 {
		return "0"
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	// For attempt counts up to 99 — Edge retries are tightly bounded;
	// anything larger collapses to a sentinel.
	if n >= 100 {
		return "99+"
	}
	return string([]byte{byte('0' + n/10), byte('0' + n%10)})
}

// ExecutionLogAttrs builds the bounded slog.Attr slice the Edge handlers
// and agentd should use when logging an AgentExecution lifecycle event.
// Only safe, bounded fields are included: tenant/session/execution/job/
// workflow/step/trace/worker IDs, normalized adapter/mode/status,
// started_at/ended_at when present, and the bounded ExecutionMetrics
// counters. Raw Labels MUST NOT be emitted by this helper — they can
// carry user-supplied values; callers wanting per-label attrs must
// allowlist them upstream.
func ExecutionLogAttrs(exec AgentExecution) []slog.Attr {
	attrs := make([]slog.Attr, 0, 16)
	if v := strings.TrimSpace(exec.TenantID); v != "" {
		attrs = append(attrs, slog.String("tenant_id", boundedID(v)))
	}
	if v := strings.TrimSpace(exec.SessionID); v != "" {
		attrs = append(attrs, slog.String("session_id", boundedID(v)))
	}
	if v := strings.TrimSpace(exec.ExecutionID); v != "" {
		attrs = append(attrs, slog.String("execution_id", boundedID(v)))
	}
	if v := strings.TrimSpace(string(exec.Adapter)); v != "" {
		attrs = append(attrs, slog.String("adapter", boundedShortString(v, 32)))
	}
	if v := strings.TrimSpace(string(exec.Mode)); v != "" {
		attrs = append(attrs, slog.String("mode", boundedShortString(v, 32)))
	}
	if v := strings.TrimSpace(exec.WorkflowRunID); v != "" {
		attrs = append(attrs, slog.String("workflow_run_id", boundedID(v)))
	}
	if v := strings.TrimSpace(exec.StepID); v != "" {
		attrs = append(attrs, slog.String("step_id", boundedShortString(v, 64)))
	}
	if v := strings.TrimSpace(exec.JobID); v != "" {
		attrs = append(attrs, slog.String("job_id", boundedID(v)))
	}
	if exec.Attempt != 0 {
		attrs = append(attrs, slog.Int("attempt", exec.Attempt))
	}
	if v := strings.TrimSpace(exec.TraceID); v != "" {
		attrs = append(attrs, slog.String("trace_id", boundedID(v)))
	}
	if v := strings.TrimSpace(exec.WorkerID); v != "" {
		attrs = append(attrs, slog.String("worker_id", boundedID(v)))
	}
	if v := strings.TrimSpace(string(exec.Status)); v != "" {
		attrs = append(attrs, slog.String("status", boundedShortString(v, 32)))
	}
	if !exec.StartedAt.IsZero() {
		attrs = append(attrs, slog.Time("started_at", exec.StartedAt))
	}
	if exec.EndedAt != nil && !exec.EndedAt.IsZero() {
		attrs = append(attrs, slog.Time("ended_at", *exec.EndedAt))
	}
	// Metrics counters — bounded by definition (int counts, single float
	// for cost). Always include so a zero-valued execution still surfaces
	// the contract; the structured-log consumer can filter zero counts.
	attrs = append(attrs,
		slog.Int("events", exec.Metrics.Events),
		slog.Int("allow", exec.Metrics.Allow),
		slog.Int("deny", exec.Metrics.Deny),
		slog.Int("require_approval", exec.Metrics.RequireApproval),
		slog.Int("artifacts", exec.Metrics.Artifacts),
		slog.Float64("llm_cost_usd", exec.Metrics.LLMCostUSD),
	)
	return attrs
}

// ApprovalLogAttrs builds the bounded slog.Attr slice for an EdgeApproval
// lifecycle event. Approval IDs/principal/resolver/rule/policy/hashes are
// length-bounded; status/decision are normalized; created_at/expires_at/
// resolved_at/consumed_at are emitted when present. Raw Reason and
// ResolutionReason fields are NEVER logged here — they can carry
// user-supplied prose with PII; callers wanting a reason in logs must
// run EDGE-004 redaction first and pass the redacted value through a
// separate slog.String.
func ApprovalLogAttrs(apr EdgeApproval) []slog.Attr {
	attrs := make([]slog.Attr, 0, 16)
	if v := strings.TrimSpace(apr.ApprovalRef); v != "" {
		attrs = append(attrs, slog.String("approval_ref", boundedShortString(v, 64)))
	}
	if v := strings.TrimSpace(apr.TenantID); v != "" {
		attrs = append(attrs, slog.String("tenant_id", boundedID(v)))
	}
	if v := strings.TrimSpace(apr.SessionID); v != "" {
		attrs = append(attrs, slog.String("session_id", boundedID(v)))
	}
	if v := strings.TrimSpace(apr.ExecutionID); v != "" {
		attrs = append(attrs, slog.String("execution_id", boundedID(v)))
	}
	if v := strings.TrimSpace(apr.EventID); v != "" {
		attrs = append(attrs, slog.String("event_id", boundedID(v)))
	}
	if v := strings.TrimSpace(apr.PrincipalID); v != "" {
		attrs = append(attrs, slog.String("principal_id", boundedID(v)))
	}
	if v := strings.TrimSpace(apr.ResolverID); v != "" {
		attrs = append(attrs, slog.String("resolver_id", boundedID(v)))
	}
	if v := strings.TrimSpace(string(apr.Status)); v != "" {
		attrs = append(attrs, slog.String("status", boundedShortString(v, 32)))
	}
	if v := strings.TrimSpace(string(apr.Decision)); v != "" {
		attrs = append(attrs, slog.String("decision", NormalizeApprovalOutcome(v)))
	}
	if v := strings.TrimSpace(apr.RuleID); v != "" {
		attrs = append(attrs, slog.String("rule_id", boundedShortString(v, 80)))
	}
	if v := strings.TrimSpace(apr.PolicySnapshot); v != "" {
		attrs = append(attrs, slog.String("policy_snapshot", boundedShortString(v, 80)))
	}
	if v := strings.TrimSpace(apr.ActionHash); v != "" {
		attrs = append(attrs, slog.String("action_hash", boundedShortString(v, 80)))
	}
	if v := strings.TrimSpace(apr.InputHash); v != "" {
		attrs = append(attrs, slog.String("input_hash", boundedShortString(v, 80)))
	}
	if !apr.CreatedAt.IsZero() {
		attrs = append(attrs, slog.Time("created_at", apr.CreatedAt))
	}
	if apr.ExpiresAt != nil && !apr.ExpiresAt.IsZero() {
		attrs = append(attrs, slog.Time("expires_at", *apr.ExpiresAt))
	}
	if apr.ResolvedAt != nil && !apr.ResolvedAt.IsZero() {
		attrs = append(attrs, slog.Time("resolved_at", *apr.ResolvedAt))
	}
	if apr.ConsumedAt != nil && !apr.ConsumedAt.IsZero() {
		attrs = append(attrs, slog.Time("consumed_at", *apr.ConsumedAt))
	}
	return attrs
}

// ExportResultLogAttrs builds the bounded slog.Attr slice for an artifact
// export operation. Artifact_type and result are bounded via the step-7
// helpers; sha256 is length-bounded; URI is NOT logged in full because it
// commonly carries signed-URL query strings — only the host portion
// (without query) is recorded if present, and even that is bounded.
func ExportResultLogAttrs(pointer ArtifactPointer, result string) []slog.Attr {
	attrs := make([]slog.Attr, 0, 10)
	if v := strings.TrimSpace(pointer.TenantID); v != "" {
		attrs = append(attrs, slog.String("tenant_id", boundedID(v)))
	}
	if v := strings.TrimSpace(pointer.SessionID); v != "" {
		attrs = append(attrs, slog.String("session_id", boundedID(v)))
	}
	if v := strings.TrimSpace(pointer.ExecutionID); v != "" {
		attrs = append(attrs, slog.String("execution_id", boundedID(v)))
	}
	if v := strings.TrimSpace(pointer.EventID); v != "" {
		attrs = append(attrs, slog.String("event_id", boundedID(v)))
	}
	attrs = append(attrs,
		slog.String("artifact_type", boundedArtifactType(string(pointer.ArtifactType))),
		slog.String("result", boundedResult(result)),
	)
	if v := strings.TrimSpace(string(pointer.RetentionClass)); v != "" {
		attrs = append(attrs, slog.String("retention_class", boundedShortString(v, 32)))
	}
	if v := strings.TrimSpace(string(pointer.RedactionLevel)); v != "" {
		attrs = append(attrs, slog.String("redaction_level", boundedShortString(v, 32)))
	}
	if v := strings.TrimSpace(pointer.SHA256); v != "" {
		attrs = append(attrs, slog.String("sha256", boundedShortString(v, 80)))
	}
	if !pointer.CreatedAt.IsZero() {
		attrs = append(attrs, slog.Time("created_at", pointer.CreatedAt))
	}
	return attrs
}

// HookSummary captures a single Edge hook handler outcome for structured
// logging. All free-form callers MUST populate fields here rather than
// passing raw error strings or untrusted ToolName values into slog —
// HookSummaryLogAttrs is the bounded contract.
type HookSummary struct {
	TenantID   string
	SessionID  string
	HookEvent  string // PreToolUse / PostToolUse / etc. (Claude PascalCase)
	Decision   string
	ReasonCode string
	LatencyMS  int64
	Mode       string
	Component  string // gateway / agentd / hook / safety_kernel / etc.
}

// HookSummaryLogAttrs returns the bounded slog.Attr slice for a hook
// outcome. Decision normalized via NormalizeDecision; hook_event passes
// through the documented Claude PascalCase passthrough; reason_code/mode/
// component bounded via step-7 helpers.
func HookSummaryLogAttrs(s HookSummary) []slog.Attr {
	attrs := make([]slog.Attr, 0, 8)
	if v := strings.TrimSpace(s.TenantID); v != "" {
		attrs = append(attrs, slog.String("tenant_id", boundedID(v)))
	}
	if v := strings.TrimSpace(s.SessionID); v != "" {
		attrs = append(attrs, slog.String("session_id", boundedID(v)))
	}
	attrs = append(attrs,
		slog.String("hook_event", boundedHookEvent(s.HookEvent)),
		slog.String("decision", NormalizeDecision(s.Decision)),
		slog.String("reason_code", boundedReasonCode(s.ReasonCode)),
		slog.Int64("latency_ms", s.LatencyMS),
		slog.String("mode", boundedMode(s.Mode)),
		slog.String("component", boundedComponent(s.Component)),
	)
	return attrs
}

// EvaluateSummary captures a single Edge evaluate handler outcome for
// structured logging. Same bounded-contract role as HookSummary.
type EvaluateSummary struct {
	TenantID    string
	SessionID   string
	ExecutionID string
	Layer       string
	Kind        string
	Decision    string
	ApprovalRef string
	LatencyMS   int64
	Mode        string
	Cached      bool
}

// EvaluateSummaryLogAttrs returns the bounded slog.Attr slice for an
// evaluate handler outcome. Layer/Kind/Decision/Mode normalized via the
// step-3/step-7 helpers; approval_ref length-bounded; cached emitted as
// bool.
func EvaluateSummaryLogAttrs(s EvaluateSummary) []slog.Attr {
	attrs := make([]slog.Attr, 0, 10)
	if v := strings.TrimSpace(s.TenantID); v != "" {
		attrs = append(attrs, slog.String("tenant_id", boundedID(v)))
	}
	if v := strings.TrimSpace(s.SessionID); v != "" {
		attrs = append(attrs, slog.String("session_id", boundedID(v)))
	}
	if v := strings.TrimSpace(s.ExecutionID); v != "" {
		attrs = append(attrs, slog.String("execution_id", boundedID(v)))
	}
	attrs = append(attrs,
		slog.String("layer", NormalizeLayer(s.Layer)),
		slog.String("kind", NormalizeKind(s.Kind)),
		slog.String("decision", NormalizeDecision(s.Decision)),
		slog.String("approval_ref", boundedShortString(s.ApprovalRef, 64)),
		slog.Int64("latency_ms", s.LatencyMS),
		slog.String("mode", boundedMode(s.Mode)),
		slog.Bool("cached", s.Cached),
	)
	return attrs
}

// ErrorLogAttrs converts a raw Go error into a bounded slog.Attr pair:
//
//   - reason_code: a short, snake_case-ish code suitable as a metric
//     label and as an audit reason. Empty input collapses to "unknown";
//     anything that fails the boundedReasonCode allowlist collapses to
//     "other".
//   - error_message: a length-bounded redacted message. The raw error
//     string is clamped to 256 chars; longer strings are truncated with
//     a "…" suffix. This intentionally does NOT run EDGE-004 redaction
//     (we don't have a redactor in this package); callers logging
//     untrusted error chains MUST redact upstream and pass the redacted
//     value via the reason_code path instead.
//
// Returns an empty slice when err is nil — safe to call unconditionally
// in error paths.
func ErrorLogAttrs(err error, reasonCode string) []slog.Attr {
	if err == nil && strings.TrimSpace(reasonCode) == "" {
		return nil
	}
	attrs := make([]slog.Attr, 0, 2)
	attrs = append(attrs, slog.String("reason_code", boundedReasonCode(reasonCode)))
	if err != nil {
		msg := redactLogMessage(err.Error())
		// 256-byte total cap including the 3-byte "…" suffix so callers
		// can rely on the slog attr fitting in a single line buffer.
		const maxBodyLen = 253
		if len(msg) > maxBodyLen {
			msg = msg[:maxBodyLen] + "…"
		}
		attrs = append(attrs, slog.String("error_message", msg))
	}
	return attrs
}

func redactLogMessage(message string) string {
	result, err := RedactValue(message, RedactionOptions{
		HashMode:       RedactionHashNone,
		MaxStringBytes: 512,
		MaxTotalBytes:  512,
	})
	if err != nil {
		return "<redacted:error>"
	}
	if out, ok := result.Value.(string); ok {
		return out
	}
	return "<redacted>"
}
