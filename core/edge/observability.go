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
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
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

	// Action decisions.
	RecordActionDecision(tenant, layer, kind, decision, mode string)
	RecordActionDenied(tenant, layer, kind, reasonCode string)

	// Approval lifecycle.
	RecordApprovalRequested(tenant, layer, kind string)
	RecordApprovalResolved(tenant, layer, kind, outcome string) // approved | rejected | expired | timeout | invalidated

	// Degraded / fail-closed outcomes.
	RecordDegraded(tenant, mode, component, reasonCode string)
	RecordFailClosed(tenant, mode, reasonCode string)

	// Artifact / export observability.
	RecordArtifactExport(tenant, artifactType, result string)

	// Latency observation.
	ObserveHookLatency(tenant, hookEvent, decision string, duration time.Duration)
	ObserveEvaluateLatency(tenant, layer, kind, decision string, duration time.Duration)

	// Cache observability (no-op until EDGE-018 wires it).
	RecordCacheLookup(tenant, layer, kind, result string) // hit | miss | miss_no_eligibility | invalidated

	// Stream observability.
	AddStreamClients(tenant string, delta int)
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
func (NoopRecorder) RecordActionDecision(string, string, string, string, string) {}
func (NoopRecorder) RecordActionDenied(string, string, string, string)           {}
func (NoopRecorder) RecordApprovalRequested(string, string, string)              {}
func (NoopRecorder) RecordApprovalResolved(string, string, string, string)       {}
func (NoopRecorder) RecordDegraded(string, string, string, string)               {}
func (NoopRecorder) RecordFailClosed(string, string, string)                     {}
func (NoopRecorder) RecordArtifactExport(string, string, string)                 {}
func (NoopRecorder) ObserveHookLatency(string, string, string, time.Duration)    {}
func (NoopRecorder) ObserveEvaluateLatency(string, string, string, string, time.Duration) {
}
func (NoopRecorder) RecordCacheLookup(string, string, string, string) {}
func (NoopRecorder) AddStreamClients(string, int)                     {}
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
// rule_id (bounded), approval_ref. Raw InputRedacted/Labels/Reason MUST NOT
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
			"mode":        boundedShortString(mode, 32),
			"component":   boundedShortString(component, 32),
			"reason_code": boundedShortString(reasonCode, 64),
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
			"mode":        boundedShortString(mode, 32),
			"component":   boundedShortString(component, 32),
			"reason_code": boundedShortString(reasonCode, 64),
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
	defer func() {
		// AuditSender.Send is documented as non-error-returning, but we
		// guard against panics defensively because audit-pipeline outage
		// must not kill the calling request.
		_ = recover()
	}()
	sender.Send(event)
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
	if v := strings.TrimSpace(event.ApprovalRef); v != "" {
		extra["approval_ref"] = boundedShortString(v, 64)
	}
	return extra
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
