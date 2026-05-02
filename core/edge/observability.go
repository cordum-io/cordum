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
