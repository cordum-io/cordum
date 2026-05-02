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

import "time"

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
