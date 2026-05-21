package edge

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// PrometheusRecorder is the Recorder implementation backed by a
// Prometheus registry. Every metric label flows through the bounded
// Normalize* helpers from observability.go before reaching
// WithLabelValues, so a high-cardinality or attacker-supplied input
// (raw command, error string, secret) cannot blow up label space or
// leak into a metric name.
//
// Tenant is intentionally NOT a metric label. The architect's allowed
// label classes (msg-cbaadcf0 + the EDGE-014 plan) are: layer, kind,
// decision, mode, agent_product, status/outcome, fail_mode,
// reason_code, artifact_type/result, hook_event, cache_result. Tenant
// identification is recovered via correlated logs/audit events; metrics
// stay tenant-agnostic to bound cardinality.
type PrometheusRecorder struct {
	sessionsCreated           *prometheus.CounterVec
	sessionsEnded             *prometheus.CounterVec
	sessionsActive            *prometheus.GaugeVec
	executionsStarted         *prometheus.CounterVec
	executionsEnded           *prometheus.CounterVec
	executionAborts           *prometheus.CounterVec
	sessionCleanupDuration    prometheus.Histogram
	sessionCleanupKeysDeleted prometheus.Counter
	sessionCleanupDeadlines   prometheus.Counter
	sessionEventCapRejected   prometheus.Counter
	sessionSwept              prometheus.Counter
	eventsPersisted           *prometheus.CounterVec
	eventsRedacted            *prometheus.CounterVec
	hookTimeouts              *prometheus.CounterVec
	actionDecisions           *prometheus.CounterVec
	actionsDenied             *prometheus.CounterVec
	approvalRequested         *prometheus.CounterVec
	approvalResolved          *prometheus.CounterVec
	approvalSweepDuration     prometheus.Histogram
	approvalSweepExpired      prometheus.Counter
	approvalEnqueueAborts     *prometheus.CounterVec
	appendEventsAborts        *prometheus.CounterVec
	idempotencyTTLExtended    *prometheus.CounterVec
	idempotencyWindowExpired  *prometheus.CounterVec
	runtimeReplayFirstSeen    *prometheus.CounterVec
	runtimeReplayReplayed     *prometheus.CounterVec
	runtimeReplayWindowFull   *prometheus.CounterVec
	degraded                  *prometheus.CounterVec
	failClosed                *prometheus.CounterVec
	agentdResponseWriteAborts *prometheus.CounterVec
	agentdShutdownForced      *prometheus.CounterVec
	edgeExportRejected        *prometheus.CounterVec
	redactionFailed           *prometheus.CounterVec
	artifactExports           *prometheus.CounterVec
	hookLatency               *prometheus.HistogramVec
	evaluateLatency           *prometheus.HistogramVec
	cacheLookups              *prometheus.CounterVec
	streamClients             prometheus.Gauge
	streamEventsSent          *prometheus.CounterVec
	streamDrops               *prometheus.CounterVec
}

// NewPrometheusRecorder allocates and registers Edge metrics on the given
// registerer. Pass `prometheus.DefaultRegisterer` in production. Tests pass
// `prometheus.NewRegistry()` so each test gets a fresh registry without
// MustRegister panics.
//
// Returns the registered Recorder. Callers do not need to retain the
// concrete type; everything is exposed through the Recorder interface.
func NewPrometheusRecorder(reg prometheus.Registerer) Recorder {
	if reg == nil {
		return NewNoopRecorder()
	}
	const ns = "cordum_edge"
	r := &PrometheusRecorder{
		sessionsCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "sessions_created_total",
			Help: "Edge sessions created, labeled by mode and agent_product.",
		}, []string{"mode", "agent_product"}),
		sessionsEnded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "sessions_ended_total",
			Help: "Edge sessions ended, labeled by mode and terminal status.",
		}, []string{"mode", "status"}),
		sessionsActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "sessions_active",
			Help: "Active Edge sessions, labeled by mode.",
		}, []string{"mode"}),
		executionsStarted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "executions_started_total",
			Help: "Edge agent executions started, labeled by mode and agent_product.",
		}, []string{"mode", "agent_product"}),
		executionsEnded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "executions_ended_total",
			Help: "Edge agent executions ended, labeled by mode and terminal status.",
		}, []string{"mode", "status"}),
		// EDGE-054 — counter for orphan-prevention aborts in CreateExecution.
		// `reason` collapses to {"parent_terminal", "parent_missing", "other",
		// "unknown"} via boundedCreateExecutionAbortReason. Bounded cardinality.
		executionAborts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "create_execution_aborted_total",
			Help: "Edge CreateExecution aborts due to parent session terminal or missing, labeled by bounded reason.",
		}, []string{"reason"}),
		sessionCleanupDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: ns, Name: "session_cleanup_duration_seconds",
			Help:    "Duration of bounded Edge session cleanup attempts.",
			Buckets: prometheus.DefBuckets,
		}),
		sessionCleanupKeysDeleted: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: ns, Name: "session_cleanup_keys_deleted_total",
			Help: "Redis keys deleted by bounded Edge session cleanup.",
		}),
		sessionCleanupDeadlines: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: ns, Name: "session_cleanup_deadline_total",
			Help: "Edge session cleanup attempts that reached the bounded cleanup deadline.",
		}),
		sessionEventCapRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: ns, Name: "session_event_cap_rejected_total",
			Help: "Edge event append attempts rejected because an execution reached the per-execution event cap.",
		}),
		sessionSwept: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: ns, Name: "session_swept_total",
			Help: "Edge sessions removed by the retention sweeper.",
		}),
		eventsPersisted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "event_persisted_total",
			Help: "Edge action events successfully persisted after store commit, labeled by layer, kind, and decision.",
		}, []string{"layer", "kind", "decision"}),
		eventsRedacted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "event_redacted_total",
			Help: "Edge event redaction outcomes at request normalization boundaries, labeled by bounded outcome.",
		}, []string{"outcome"}),
		hookTimeouts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "hook_timeout_total",
			Help: "Edge hook timeout events, labeled by bounded phase.",
		}, []string{"phase"}),
		actionDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "action_decisions_total",
			Help: "Edge action policy decisions by layer, kind, decision, and mode.",
		}, []string{"layer", "kind", "decision", "mode"}),
		actionsDenied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "actions_denied_total",
			Help: "Edge actions denied, labeled by layer, kind, and bounded reason_code.",
		}, []string{"layer", "kind", "reason_code"}),
		approvalRequested: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "approvals_requested_total",
			Help: "Edge approvals requested, labeled by layer and kind.",
		}, []string{"layer", "kind"}),
		approvalResolved: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "approvals_resolved_total",
			Help: "Edge approvals resolved, labeled by layer, kind, and outcome.",
		}, []string{"layer", "kind", "outcome"}),
		approvalSweepDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: ns, Name: "approval_sweep_duration_seconds",
			Help:    "Duration of periodic Edge approval expiry sweeps.",
			Buckets: prometheus.DefBuckets,
		}),
		approvalSweepExpired: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: ns, Name: "approval_sweep_expired_total",
			Help: "Edge approvals expired by the periodic approval sweeper.",
		}),
		// EDGE-058 — counter for fail-closed EnqueueApproval aborts. `reason`
		// collapses to {"event_list_too_large", "other", "unknown"} via
		// boundedApprovalEnqueueAbortReason. Bounded cardinality.
		approvalEnqueueAborts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "approval_enqueue_aborted_total",
			Help: "Edge EnqueueApproval aborts due to fail-closed safety guards (e.g. event list too large for inline validation), labeled by bounded reason.",
		}, []string{"reason"}),
		// EDGE-055 — counter for AppendEvents aborts when the parent edge
		// session or its execution transitioned to terminal mid-flight.
		// `reason` collapses to {"parent_session_terminal",
		// "execution_terminal", "other", "unknown"} via
		// boundedAppendEventsAbortReason. Bounded cardinality.
		appendEventsAborts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "append_events_aborted_total",
			Help: "Edge AppendEvents aborts when the parent edge session or execution transitioned to terminal between request entry and the WATCH commit, labeled by bounded reason.",
		}, []string{"reason"}),
		// EDGE-061 — counter for idempotency record TTL refresh on
		// Reserve retry. `state` collapses to {"pending", "replay",
		// "other", "unknown"} via boundedIdempotencyTTLExtendedState.
		idempotencyTTLExtended: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "idempotency_ttl_extended_total",
			Help: "Edge idempotency record TTL refreshes on Reserve retry, labeled by bounded state.",
		}, []string{"state"}),
		// EDGE-061 — counter for idempotency window-expired rejections at
		// Reserve/Complete cap-check (ErrIdempotencyRecordExpired) and
		// at the existing append surface (ErrIdempotencyWindowExpired).
		// `phase` collapses to {"reserve", "complete", "append",
		// "other", "unknown"} via boundedIdempotencyWindowExpiredPhase.
		idempotencyWindowExpired: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "idempotency_window_expired_total",
			Help: "Edge idempotency window-expired rejections (max in-flight cap or duplicate-after-TTL), labeled by bounded phase.",
		}, []string{"phase"}),
		runtimeReplayFirstSeen: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "runtime_replay_first_seen_total",
			Help: "Runtime ingest batches accepted as first-seen by the replay window, labeled only by identity presence.",
		}, []string{"tenant_present", "collector_present"}),
		runtimeReplayReplayed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "runtime_replay_replayed_total",
			Help: "Runtime ingest duplicate batches suppressed by the replay window, labeled only by identity presence.",
		}, []string{"tenant_present", "collector_present"}),
		runtimeReplayWindowFull: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "runtime_replay_window_full_total",
			Help: "Runtime ingest batches refused because the replay window reached its cardinality cap, labeled only by identity presence.",
		}, []string{"tenant_present", "collector_present"}),
		degraded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "degraded_total",
			Help: "Edge degraded outcomes, labeled by mode, component, and reason_code.",
		}, []string{"mode", "component", "reason_code"}),
		failClosed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "fail_closed_total",
			Help: "Edge fail-closed outcomes, labeled by mode and reason_code.",
		}, []string{"mode", "reason_code"}),
		// EDGE-059 — counter for agentd local-server response-write aborts.
		// Fires when the JSON encoder reports a write error (typically due to
		// http.Server.WriteTimeout firing on a slow-reading client).
		agentdResponseWriteAborts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "agentd_response_write_aborted_total",
			Help: "agentd local-server response-write aborts (slow-loris guard), labeled by bounded reason.",
		}, []string{"reason"}),
		// EDGE-063 — counter for agentd shutdown sub-component force-exits
		// when graceful drain timed out. `reason` collapses via
		// boundedAgentdShutdownForcedReason to {"http_server_drain",
		// "heartbeat_drain", "other", "unknown"}.
		agentdShutdownForced: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "agentd_shutdown_forced_total",
			Help: "agentd shutdown forced exits when a sub-component's graceful drain timed out, labeled by bounded reason.",
		}, []string{"reason"}),
		// EDGE-065 — counter for Edge session-export request-validation
		// rejections (max_events upper bound, future request-shape
		// rejections). `reason` collapses via boundedEdgeExportRejectedReason.
		edgeExportRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "export_request_rejected_total",
			Help: "Edge session-export request-validation rejections, labeled by bounded reason.",
		}, []string{"reason"}),
		// EDGE-071 — counter for redaction call-site fail-closed events.
		// Fires when an Edge redaction site returns the safe placeholder
		// because the underlying redactor errored or the input exceeded
		// MaxRedactionInputBytes. `site` identifies the call site;
		// `reason` describes the failure mode. Both labels collapse via
		// boundedRedactionFailedSite / boundedRedactionFailedReason.
		// Bounded cardinality.
		redactionFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "redaction_failed_total",
			Help: "Edge redaction fail-closed events (redactor error or input too large), labeled by bounded site and reason.",
		}, []string{"site", "reason"}),
		artifactExports: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "artifact_exports_total",
			Help: "Edge artifact exports, labeled by artifact_type and result.",
		}, []string{"artifact_type", "result"}),
		hookLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: ns, Name: "hook_latency_seconds",
			Help:    "Claude hook end-to-end latency, labeled by hook_event and decision.",
			Buckets: prometheus.DefBuckets,
		}, []string{"hook_event", "decision"}),
		evaluateLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: ns, Name: "evaluate_latency_seconds",
			Help:    "Edge evaluate latency, labeled by layer, kind, and decision.",
			Buckets: prometheus.DefBuckets,
		}, []string{"layer", "kind", "decision"}),
		cacheLookups: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "cache_lookups_total",
			Help: "Edge agentd safe-allow cache lookups, labeled by layer, kind, and result.",
		}, []string{"layer", "kind", "result"}),
		streamClients: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: ns, Name: "stream_clients",
			Help: "Active Edge stream WebSocket clients (sum across tenants).",
		}),
		streamEventsSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "ws_events_sent_total",
			Help: "Edge WebSocket events accepted into the broadcast queue, labeled only by tenant_present to avoid cardinality.",
		}, []string{"tenant_present"}),
		streamDrops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "stream_drops_total",
			Help: "Edge stream events dropped, labeled by bounded reason.",
		}, []string{"reason"}),
	}
	reg.MustRegister(
		r.sessionsCreated, r.sessionsEnded, r.sessionsActive,
		r.executionsStarted, r.executionsEnded, r.executionAborts,
		r.sessionCleanupDuration, r.sessionCleanupKeysDeleted,
		r.sessionCleanupDeadlines, r.sessionEventCapRejected,
		r.sessionSwept,
		r.eventsPersisted, r.eventsRedacted, r.hookTimeouts,
		r.actionDecisions, r.actionsDenied,
		r.approvalRequested, r.approvalResolved, r.approvalSweepDuration,
		r.approvalSweepExpired, r.approvalEnqueueAborts,
		r.appendEventsAborts,
		r.idempotencyTTLExtended, r.idempotencyWindowExpired,
		r.runtimeReplayFirstSeen, r.runtimeReplayReplayed, r.runtimeReplayWindowFull,
		r.degraded, r.failClosed, r.agentdResponseWriteAborts,
		r.agentdShutdownForced,
		r.edgeExportRejected,
		r.redactionFailed,
		r.artifactExports,
		r.hookLatency, r.evaluateLatency,
		r.cacheLookups,
		r.streamClients, r.streamEventsSent, r.streamDrops,
	)
	return r
}

func (r *PrometheusRecorder) RecordSessionCreated(_ /*tenant*/, mode, agentProduct string) {
	r.sessionsCreated.WithLabelValues(boundedMode(mode), boundedAgentProduct(agentProduct)).Inc()
}
func (r *PrometheusRecorder) RecordSessionEnded(_ /*tenant*/, mode, status string) {
	r.sessionsEnded.WithLabelValues(boundedMode(mode), boundedStatus(status)).Inc()
}
func (r *PrometheusRecorder) SetSessionsActive(_ /*tenant*/, mode string, count int) {
	r.sessionsActive.WithLabelValues(boundedMode(mode)).Set(float64(count))
}
func (r *PrometheusRecorder) RecordExecutionStarted(_ /*tenant*/, mode, agentProduct string) {
	r.executionsStarted.WithLabelValues(boundedMode(mode), boundedAgentProduct(agentProduct)).Inc()
}
func (r *PrometheusRecorder) RecordExecutionEnded(_ /*tenant*/, mode, status string) {
	r.executionsEnded.WithLabelValues(boundedMode(mode), boundedStatus(status)).Inc()
}
func (r *PrometheusRecorder) RecordCreateExecutionAborted(reason string) {
	r.executionAborts.WithLabelValues(boundedCreateExecutionAbortReason(reason)).Inc()
}
func (r *PrometheusRecorder) ObserveSessionCleanupDuration(duration time.Duration) {
	r.sessionCleanupDuration.Observe(duration.Seconds())
}
func (r *PrometheusRecorder) AddSessionCleanupKeysDeleted(count int) {
	if count > 0 {
		r.sessionCleanupKeysDeleted.Add(float64(count))
	}
}
func (r *PrometheusRecorder) RecordSessionCleanupDeadline() {
	r.sessionCleanupDeadlines.Inc()
}
func (r *PrometheusRecorder) RecordSessionEventCapRejected() {
	r.sessionEventCapRejected.Inc()
}
func (r *PrometheusRecorder) RecordSessionSwept() {
	r.sessionSwept.Inc()
}
func (r *PrometheusRecorder) RecordEventPersisted(_ /*tenant*/, layer, kind, decision string) {
	r.eventsPersisted.WithLabelValues(NormalizeLayer(layer), NormalizeKind(kind), NormalizeDecision(decision)).Inc()
}
func (r *PrometheusRecorder) RecordEventRedacted(outcome string) {
	r.eventsRedacted.WithLabelValues(NormalizeRedactionStatus(outcome)).Inc()
}
func (r *PrometheusRecorder) RecordHookTimeout(phase string) {
	r.hookTimeouts.WithLabelValues(boundedHookTimeoutPhase(phase)).Inc()
}
func (r *PrometheusRecorder) RecordActionDecision(_ /*tenant*/, layer, kind, decision, mode string) {
	r.actionDecisions.WithLabelValues(NormalizeLayer(layer), NormalizeKind(kind), NormalizeDecision(decision), boundedMode(mode)).Inc()
}
func (r *PrometheusRecorder) RecordActionDenied(_ /*tenant*/, layer, kind, reasonCode string) {
	r.actionsDenied.WithLabelValues(NormalizeLayer(layer), NormalizeKind(kind), boundedReasonCode(reasonCode)).Inc()
}
func (r *PrometheusRecorder) RecordApprovalRequested(_ /*tenant*/, layer, kind string) {
	r.approvalRequested.WithLabelValues(NormalizeLayer(layer), NormalizeKind(kind)).Inc()
}
func (r *PrometheusRecorder) RecordApprovalResolved(_ /*tenant*/, layer, kind, outcome string) {
	r.approvalResolved.WithLabelValues(NormalizeLayer(layer), NormalizeKind(kind), NormalizeApprovalOutcome(outcome)).Inc()
}
func (r *PrometheusRecorder) ObserveApprovalSweepDuration(duration time.Duration) {
	r.approvalSweepDuration.Observe(duration.Seconds())
}
func (r *PrometheusRecorder) AddApprovalSweepExpired(count int) {
	if count > 0 {
		r.approvalSweepExpired.Add(float64(count))
	}
}
func (r *PrometheusRecorder) RecordApprovalEnqueueAborted(reason string) {
	r.approvalEnqueueAborts.WithLabelValues(boundedApprovalEnqueueAbortReason(reason)).Inc()
}
func (r *PrometheusRecorder) RecordAppendEventsAborted(reason string) {
	r.appendEventsAborts.WithLabelValues(boundedAppendEventsAbortReason(reason)).Inc()
}
func (r *PrometheusRecorder) RecordIdempotencyTTLExtended(state string) {
	r.idempotencyTTLExtended.WithLabelValues(boundedIdempotencyTTLExtendedState(state)).Inc()
}
func (r *PrometheusRecorder) RecordIdempotencyWindowExpired(phase string) {
	r.idempotencyWindowExpired.WithLabelValues(boundedIdempotencyWindowExpiredPhase(phase)).Inc()
}
func (r *PrometheusRecorder) RecordRuntimeReplayFirstSeen(tenant, collector string) {
	r.runtimeReplayFirstSeen.WithLabelValues(boundedIdentityPresent(tenant), boundedIdentityPresent(collector)).Inc()
}
func (r *PrometheusRecorder) RecordRuntimeReplayReplayed(tenant, collector string) {
	r.runtimeReplayReplayed.WithLabelValues(boundedIdentityPresent(tenant), boundedIdentityPresent(collector)).Inc()
}
func (r *PrometheusRecorder) RecordRuntimeReplayWindowFull(tenant, collector string) {
	r.runtimeReplayWindowFull.WithLabelValues(boundedIdentityPresent(tenant), boundedIdentityPresent(collector)).Inc()
}
func (r *PrometheusRecorder) RecordDegraded(_ /*tenant*/, mode, component, reasonCode string) {
	r.degraded.WithLabelValues(boundedMode(mode), boundedComponent(component), boundedReasonCode(reasonCode)).Inc()
}
func (r *PrometheusRecorder) RecordFailClosed(_ /*tenant*/, mode, reasonCode string) {
	r.failClosed.WithLabelValues(boundedMode(mode), boundedReasonCode(reasonCode)).Inc()
}
func (r *PrometheusRecorder) RecordAgentdResponseWriteAborted(reason string) {
	r.agentdResponseWriteAborts.WithLabelValues(boundedAgentdResponseWriteAbortReason(reason)).Inc()
}
func (r *PrometheusRecorder) RecordAgentdShutdownForced(reason string) {
	r.agentdShutdownForced.WithLabelValues(boundedAgentdShutdownForcedReason(reason)).Inc()
}
func (r *PrometheusRecorder) RecordRedactionFailed(site, reason string) {
	r.redactionFailed.WithLabelValues(boundedRedactionFailedSite(site), boundedRedactionFailedReason(reason)).Inc()
}
func (r *PrometheusRecorder) RecordEdgeExportRequestRejected(reason string) {
	r.edgeExportRejected.WithLabelValues(boundedEdgeExportRejectedReason(reason)).Inc()
}
func (r *PrometheusRecorder) RecordArtifactExport(_ /*tenant*/, artifactType, result string) {
	r.artifactExports.WithLabelValues(boundedArtifactType(artifactType), boundedResult(result)).Inc()
}
func (r *PrometheusRecorder) ObserveHookLatency(_ /*tenant*/, hookEvent, decision string, duration time.Duration) {
	r.hookLatency.WithLabelValues(boundedHookEvent(hookEvent), NormalizeDecision(decision)).Observe(duration.Seconds())
}
func (r *PrometheusRecorder) ObserveEvaluateLatency(_ /*tenant*/, layer, kind, decision string, duration time.Duration) {
	r.evaluateLatency.WithLabelValues(NormalizeLayer(layer), NormalizeKind(kind), NormalizeDecision(decision)).Observe(duration.Seconds())
}
func (r *PrometheusRecorder) RecordCacheLookup(_ /*tenant*/, layer, kind, result string) {
	r.cacheLookups.WithLabelValues(NormalizeLayer(layer), NormalizeKind(kind), boundedCacheResult(result)).Inc()
}

// AddStreamClients is gauge-style and not labeled by tenant; stream drops
// are not labeled by tenant either to avoid cardinality blow-up if many
// tenants are filtered out.
func (r *PrometheusRecorder) AddStreamClients(_ /*tenant*/ string, delta int) {
	r.streamClients.Add(float64(delta))
}
func (r *PrometheusRecorder) RecordStreamEventSent(tenant string) {
	r.streamEventsSent.WithLabelValues(boundedTenantPresent(tenant)).Inc()
}
func (r *PrometheusRecorder) RecordStreamDrop(reason string) {
	r.streamDrops.WithLabelValues(NormalizeStreamDropReason(reason)).Inc()
}

// boundedMode collapses arbitrary mode strings to the documented enum.
// Anything unrecognized -> "other".
var allowedModes = map[string]struct{}{
	"observe":           {},
	"local-dev":         {},
	"local-dev-enforce": {},
	"enterprise-strict": {},
	"workflow":          {}, // workflow requires=edge-governance
}

func boundedMode(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedModes[v]; ok {
		return v
	}
	return "other"
}

func boundedTenantPresent(value string) string {
	return boundedIdentityPresent(value)
}

func boundedIdentityPresent(value string) string {
	if lowerTrim(value) == "" {
		return "false"
	}
	return "true"
}

var allowedHookTimeoutPhases = map[string]struct{}{
	"request": {},
	"gateway": {},
	"kernel":  {},
}

func boundedHookTimeoutPhase(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedHookTimeoutPhases[v]; ok {
		return v
	}
	return "other"
}

var allowedAgentProducts = map[string]struct{}{
	"claude-code": {},
	"codex":       {},
	"cursor":      {},
}

func boundedAgentProduct(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedAgentProducts[v]; ok {
		return v
	}
	return "other"
}

var allowedStatuses = map[string]struct{}{
	"starting":             {},
	"running":              {},
	"waiting_for_approval": {},
	"degraded":             {},
	"ended":                {},
	"failed":               {},
	"succeeded":            {},
	"cancelled":            {},
	"timeout":              {},
	"ok":                   {},
	"blocked":              {},
}

func boundedStatus(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedStatuses[v]; ok {
		return v
	}
	return "other"
}

var allowedComponents = map[string]struct{}{
	"gateway":        {},
	"agentd":         {},
	"hook":           {},
	"safety_kernel":  {},
	"approvals":      {},
	"event_store":    {},
	"audit":          {},
	"artifact_store": {},
	"stream":         {},
}

func boundedComponent(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedComponents[v]; ok {
		return v
	}
	return "other"
}

// boundedReasonCode allows any short snake_case-ish code but rejects
// long/free-form input. Reason codes are emitted by Edge call sites
// from a small documented set per surface; unknown inputs collapse to
// "other" rather than reaching a metric label.
func boundedReasonCode(value string) string {
	if _, ok := secretStringType(value); ok {
		return "other"
	}
	// Reject inputs that contain internal whitespace or control characters
	// before normalization — they indicate free-form prose ("raw command
	// with spaces", "Bearer secret") that the lowerTrim helper would
	// otherwise quietly truncate to the first word.
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			// Allow leading/trailing whitespace (lowerTrim handles
			// that); reject any internal whitespace.
			if i == 0 || i == len(value)-1 {
				continue
			}
			// Detect "non-leading non-trailing" whitespace by scanning
			// for non-whitespace before and after.
			seenContent := false
			for j := 0; j < i; j++ {
				if value[j] != ' ' && value[j] != '\t' {
					seenContent = true
					break
				}
			}
			if !seenContent {
				continue
			}
			tailHasContent := false
			for j := i + 1; j < len(value); j++ {
				if value[j] != ' ' && value[j] != '\t' {
					tailHasContent = true
					break
				}
			}
			if tailHasContent {
				return "other"
			}
		}
	}
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if len(v) > 48 {
		return "other"
	}
	for _, r := range v {
		if r == '_' || r == '-' || r == '.' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		return "other"
	}
	return v
}

var allowedArtifactTypes = map[string]struct{}{
	"edge.session_export":      {},
	"edge.action_input":        {},
	"edge.action_output":       {},
	"edge.policy_decision":     {},
	"edge.approval_record":     {},
	"edge.policy_snapshot":     {},
	"edge.audit_export":        {},
	"edge.evidence_bundle":     {},
	"edge.hook_payload":        {},
	"edge.classifier_metadata": {},
}

func boundedArtifactType(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedArtifactTypes[v]; ok {
		return v
	}
	return "other"
}

var allowedResults = map[string]struct{}{
	"ok":              {},
	"failed":          {},
	"missing":         {},
	"truncated":       {},
	"oversize":        {},
	"unauthorized":    {},
	"tenant_mismatch": {},
}

func boundedResult(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedResults[v]; ok {
		return v
	}
	return "other"
}

var allowedHookEvents = map[string]struct{}{
	"PreToolUse":         {},
	"PostToolUse":        {},
	"PostToolUseFailure": {},
	"UserPromptSubmit":   {},
	"ConfigChange":       {},
	"FileChanged":        {},
}

// boundedHookEvent passes through the documented Claude hook event names
// (case-sensitive — they are PascalCase by Claude convention) and
// collapses everything else to "other".
func boundedHookEvent(value string) string {
	v := value
	for v != "" && (v[0] == ' ' || v[0] == '\t') {
		v = v[1:]
	}
	for v != "" && (v[len(v)-1] == ' ' || v[len(v)-1] == '\t') {
		v = v[:len(v)-1]
	}
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedHookEvents[v]; ok {
		return v
	}
	return "other"
}

var allowedCacheResults = map[string]struct{}{
	"hit":                 {},
	"miss":                {},
	"miss_no_eligibility": {},
	"invalidated":         {},
	"expired":             {},
}

func boundedCacheResult(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedCacheResults[v]; ok {
		return v
	}
	return "other"
}

// allowedAgentdResponseWriteAbortReasons is the bounded set of `reason`
// label values for the EDGE-059 agentd_response_write_aborted_total counter.
//   - write_timeout: net/http server WriteTimeout fired (the slow-loris case).
//   - write_error:   any other write error from the JSON encoder.
var allowedAgentdResponseWriteAbortReasons = map[string]struct{}{
	"write_timeout": {},
	"write_error":   {},
}

func boundedAgentdResponseWriteAbortReason(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedAgentdResponseWriteAbortReasons[v]; ok {
		return v
	}
	return "other"
}

// allowedApprovalEnqueueAbortReasons is the bounded set of `reason` label
// values for the EDGE-058 approval_enqueue_aborted_total counter.
//   - event_list_too_large: parent execution's AgentActionEvent list exceeded
//     maxEventsPerApprovalValidation at the moment loadEventFromTx ran.
var allowedApprovalEnqueueAbortReasons = map[string]struct{}{
	"event_list_too_large": {},
}

func boundedApprovalEnqueueAbortReason(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedApprovalEnqueueAbortReasons[v]; ok {
		return v
	}
	return "other"
}

// allowedCreateExecutionAbortReasons is the bounded set of `reason` label
// values for the EDGE-054 create_execution_aborted_total counter.
//   - parent_terminal: parent EdgeSession was Ended/Failed at the moment
//     CreateExecution's WATCH/MULTI/EXEC ran (the original race target).
//   - parent_missing: parent EdgeSession was deleted between the initial
//     GetSession read and the WATCH commit.
var allowedCreateExecutionAbortReasons = map[string]struct{}{
	"parent_terminal": {},
	"parent_missing":  {},
}

func boundedCreateExecutionAbortReason(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedCreateExecutionAbortReasons[v]; ok {
		return v
	}
	return "other"
}

// allowedIdempotencyTTLExtendedStates is the bounded set of `state`
// label values for the EDGE-061 idempotency_ttl_extended_total counter.
//   - pending: Reserve retry observed an in-flight Pending record and
//     refreshed its TTL inside the WATCH transaction.
//   - replay: Reserve retry observed a Completed record (replay path)
//     and refreshed its TTL so further retries find it within the cap.
var allowedIdempotencyTTLExtendedStates = map[string]struct{}{
	"pending": {},
	"replay":  {},
}

func boundedIdempotencyTTLExtendedState(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedIdempotencyTTLExtendedStates[v]; ok {
		return v
	}
	return "other"
}

// allowedIdempotencyWindowExpiredPhases is the bounded set of `phase`
// label values for the EDGE-061 idempotency_window_expired_total
// counter.
//   - reserve: ReserveIdempotency rejected with ErrIdempotencyRecordExpired
//     (record older than the 7-day max-in-flight cap).
//   - complete: CompleteIdempotency rejected with ErrIdempotencyRecordExpired.
//   - append: append-with-idempotency hit the existing
//     ErrIdempotencyWindowExpired surface (duplicate event_id post-TTL).
var allowedIdempotencyWindowExpiredPhases = map[string]struct{}{
	"reserve":  {},
	"complete": {},
	"append":   {},
}

func boundedIdempotencyWindowExpiredPhase(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedIdempotencyWindowExpiredPhases[v]; ok {
		return v
	}
	return "other"
}

// allowedEdgeExportRejectedReasons is the bounded set of `reason` label
// values for the EDGE-065 export_request_rejected_total counter.
//   - max_events_too_large: caller-supplied max_events exceeded the
//     server-side cap (handlers_edge_export.go maxExportEventsRequest).
var allowedEdgeExportRejectedReasons = map[string]struct{}{
	"max_events_too_large": {},
}

func boundedEdgeExportRejectedReason(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedEdgeExportRejectedReasons[v]; ok {
		return v
	}
	return "other"
}

// allowedAgentdShutdownForcedReasons is the bounded set of `reason`
// label values for the EDGE-063 agentd_shutdown_forced_total counter.
//   - http_server_drain: httpServer.Shutdown returned but the Serve
//     goroutine did not exit before the bounded join timeout fired.
//   - heartbeat_drain: heartbeat OnStatus / RecordHeartbeatStatus
//     could not complete inside the bounded shutdown budget.
var allowedAgentdShutdownForcedReasons = map[string]struct{}{
	"http_server_drain": {},
	"heartbeat_drain":   {},
}

func boundedAgentdShutdownForcedReason(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedAgentdShutdownForcedReasons[v]; ok {
		return v
	}
	return "other"
}

// allowedAppendEventsAbortReasons is the bounded set of `reason` label
// values for the EDGE-055 append_events_aborted_total counter.
//   - parent_session_terminal: parent EdgeSession was Ended/Failed at the
//     moment AppendEvents' WATCH/MULTI/EXEC ran (the EDGE-055 widening).
//   - execution_terminal: the execution itself was Ended/Failed/Cancelled
//     at the moment refreshAppendExecutionsInTx ran (preexisting guard,
//     reused in EDGE-055).
var allowedAppendEventsAbortReasons = map[string]struct{}{
	"parent_session_terminal": {},
	"execution_terminal":      {},
}

func boundedAppendEventsAbortReason(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedAppendEventsAbortReasons[v]; ok {
		return v
	}
	return "other"
}

// allowedRedactionFailedSites is the bounded set of `site` label values
// for the EDGE-071 redaction_failed_total counter. Every Edge call site
// that emits the fail-closed placeholder must register its site name
// here; arbitrary input collapses to "other" so an attacker-supplied
// or future-added site name cannot blow up cardinality.
//   - claude.redact_hook_boundary_string: mapper.go redactHookBoundaryString
//     (the EDGE-071 fix site).
//   - gateway.edge_event_input: Gateway Edge event/evaluate input redaction.
var allowedRedactionFailedSites = map[string]struct{}{
	"claude.redact_hook_boundary_string": {},
	"gateway.edge_event_input":           {},
}

func boundedRedactionFailedSite(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedRedactionFailedSites[v]; ok {
		return v
	}
	return "other"
}

// allowedRedactionFailedReasons is the bounded set of `reason` label
// values for the EDGE-071 redaction_failed_total counter.
//   - redactor_error: edge.RedactValue (or its claude alias) returned a
//     non-nil error; today's only path is applyHashOptions but the
//     fail-closed branch protects against future regressions.
//   - input_too_large: the call site received an input larger than
//     edge.MaxRedactionInputBytes and short-circuited to the placeholder.
var allowedRedactionFailedReasons = map[string]struct{}{
	"redactor_error":  {},
	"input_too_large": {},
}

func boundedRedactionFailedReason(value string) string {
	v := lowerTrim(value)
	if v == "" {
		return "unknown"
	}
	if _, ok := allowedRedactionFailedReasons[v]; ok {
		return v
	}
	return "other"
}

// _ is a tiny sanity reference so the linter does not flag the
// recorder fields as "unused" when the impl is built without callers
// during incremental development. Callers in steps 10-12 will exercise
// every method.
var _ sync.Mutex // keep import alive if struct mutex is added later
