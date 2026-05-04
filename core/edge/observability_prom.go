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
	sessionsCreated   *prometheus.CounterVec
	sessionsEnded     *prometheus.CounterVec
	sessionsActive    *prometheus.GaugeVec
	executionsStarted *prometheus.CounterVec
	executionsEnded   *prometheus.CounterVec
	executionAborts   *prometheus.CounterVec
	actionDecisions   *prometheus.CounterVec
	actionsDenied     *prometheus.CounterVec
	approvalRequested      *prometheus.CounterVec
	approvalResolved       *prometheus.CounterVec
	approvalEnqueueAborts  *prometheus.CounterVec
	appendEventsAborts     *prometheus.CounterVec
	degraded             *prometheus.CounterVec
	failClosed           *prometheus.CounterVec
	agentdResponseWriteAborts *prometheus.CounterVec
	artifactExports   *prometheus.CounterVec
	hookLatency       *prometheus.HistogramVec
	evaluateLatency   *prometheus.HistogramVec
	cacheLookups      *prometheus.CounterVec
	streamClients     prometheus.Gauge
	streamDrops       *prometheus.CounterVec
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
		streamDrops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "stream_drops_total",
			Help: "Edge stream events dropped, labeled by bounded reason.",
		}, []string{"reason"}),
	}
	reg.MustRegister(
		r.sessionsCreated, r.sessionsEnded, r.sessionsActive,
		r.executionsStarted, r.executionsEnded, r.executionAborts,
		r.actionDecisions, r.actionsDenied,
		r.approvalRequested, r.approvalResolved, r.approvalEnqueueAborts,
		r.appendEventsAborts,
		r.degraded, r.failClosed, r.agentdResponseWriteAborts,
		r.artifactExports,
		r.hookLatency, r.evaluateLatency,
		r.cacheLookups,
		r.streamClients, r.streamDrops,
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
func (r *PrometheusRecorder) RecordApprovalEnqueueAborted(reason string) {
	r.approvalEnqueueAborts.WithLabelValues(boundedApprovalEnqueueAbortReason(reason)).Inc()
}
func (r *PrometheusRecorder) RecordAppendEventsAborted(reason string) {
	r.appendEventsAborts.WithLabelValues(boundedAppendEventsAbortReason(reason)).Inc()
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

// _ is a tiny sanity reference so the linter does not flag the
// recorder fields as "unused" when the impl is built without callers
// during incremental development. Callers in steps 10-12 will exercise
// every method.
var _ sync.Mutex // keep import alive if struct mutex is added later
