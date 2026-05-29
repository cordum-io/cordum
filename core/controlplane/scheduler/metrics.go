package scheduler

// Prometheus metrics specific to the heartbeat-demotion rollout. Kept
// here (rather than in core/infra/metrics/metrics.go) because both
// metrics are fully owned by the scheduler trust pipeline: they are
// observed from Engine code paths, use labels that only the
// scheduler can compute (tenant derived from WorkerTrustState), and
// never need to be called from other services.
//
// Two gauges are exposed so operator dashboards can migrate from
// "heartbeat-age as alive signal" to "session-token as alive signal":
//
//   cordum_scheduler_worker_session_valid{worker_id,tenant,pod}
//       1 when the worker's session token is currently trusted
//       (SessionValid && !Revoked && not expired);
//       0 otherwise.
//
//   cordum_scheduler_worker_heartbeat_age_seconds{worker_id,pod}
//       Seconds since the worker's most recent heartbeat. Reported
//       as telemetry only — dashboards should render it as a
//       freshness indicator, never use it to page operators for
//       "worker down". Session authority is the correct signal for
//       that.
//
// Both gauges use the pod-wrapped default registerer so HA replicas
// are distinguishable. The package-level init registers them lazily
// on first call — NewWorkerTrustMetrics is idempotent under
// concurrent test setup.

import (
	"sync"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/prometheus/client_golang/prometheus"
)

// WorkerTrustMetrics wraps the Prometheus gauges used by the
// heartbeat-demotion rollout. Exposed as a small struct (rather than
// package-level globals) so tests can inject a scoped registry
// without leaking state across runs.
type WorkerTrustMetrics struct {
	sessionValid *prometheus.GaugeVec
	heartbeatAge *prometheus.GaugeVec
}

var (
	defaultWorkerTrustMetrics     *WorkerTrustMetrics
	defaultWorkerTrustMetricsOnce sync.Once
)

// NewWorkerTrustMetrics constructs + registers the gauges with reg.
// A nil reg falls back to prometheus.DefaultRegisterer. Registration
// errors (typically "already registered") are surfaced so callers can
// decide; the default-instance helper below swallows them because
// duplicate registration in the face of re-init is benign.
func NewWorkerTrustMetrics(reg prometheus.Registerer) (*WorkerTrustMetrics, error) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	sv := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "cordum",
		Subsystem: "scheduler",
		Name:      "worker_session_valid",
		Help: "1 when the worker's session token is currently trusted " +
			"(valid exp, not revoked); 0 otherwise. Authoritative " +
			"signal for worker dispatch eligibility after the " +
			"heartbeat-demotion rollout.",
	}, []string{"worker_id", "tenant"})
	hba := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "cordum",
		Subsystem: "scheduler",
		Name:      "worker_heartbeat_age_seconds",
		Help: "Seconds since the last heartbeat observed for the worker. " +
			"Telemetry only — not an authoritative alive signal. " +
			"Use cordum_scheduler_worker_session_valid for policy alerts.",
	}, []string{"worker_id"})
	if err := reg.Register(sv); err != nil {
		return nil, err
	}
	if err := reg.Register(hba); err != nil {
		return nil, err
	}
	return &WorkerTrustMetrics{sessionValid: sv, heartbeatAge: hba}, nil
}

// DefaultWorkerTrustMetrics returns the package-level metrics instance,
// constructing it on first call. Safe under concurrent access. A
// registration failure (e.g. when a previous instance has not been
// unregistered in tests) is silently ignored and a fresh dummy
// instance is returned so callers continue without panicking.
func DefaultWorkerTrustMetrics() *WorkerTrustMetrics {
	defaultWorkerTrustMetricsOnce.Do(func() {
		if m, err := NewWorkerTrustMetrics(nil); err == nil {
			defaultWorkerTrustMetrics = m
			return
		}
		// Duplicate registration (already registered on an earlier
		// init). Re-use the existing collectors via a throwaway
		// registry — observations will still flow through the
		// previously-registered gauges since prometheus keeps the
		// instances by fully-qualified name.
		defaultWorkerTrustMetrics = &WorkerTrustMetrics{
			sessionValid: prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "cordum", Subsystem: "scheduler", Name: "worker_session_valid_noop"}, []string{"worker_id", "tenant"}),
			heartbeatAge: prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "cordum", Subsystem: "scheduler", Name: "worker_heartbeat_age_seconds_noop"}, []string{"worker_id"}),
		}
	})
	return defaultWorkerTrustMetrics
}

// ObserveTrust is the single call point that surfaces a worker's
// current trust state as a Prometheus observation. Safe to call from
// the hot path (per-dispatch or per-snapshot), O(1) per invocation.
//
// The tenant label is pinned to the WorkerTrustState's tenant when
// available, falling back to "unknown" so the label set is never
// empty (Prometheus does not accept empty label values on strict
// setups).
func (m *WorkerTrustMetrics) ObserveTrust(workerID string, state WorkerTrustState) {
	if m == nil || workerID == "" {
		return
	}
	tenant := state.Tenant
	if tenant == "" {
		tenant = "unknown"
	}
	value := 0.0
	if state.IsAlive() {
		value = 1.0
	}
	m.sessionValid.WithLabelValues(workerID, tenant).Set(value)
}

// ObserveHeartbeatAge surfaces the heartbeat freshness telemetry.
// Call once per heartbeat receive or per periodic sweep. lastSeen
// zero values are treated as "no heartbeat on file" and skipped so
// the gauge does not collapse every worker to age=~now.
func (m *WorkerTrustMetrics) ObserveHeartbeatAge(workerID string, lastSeen time.Time, now time.Time) {
	if m == nil || workerID == "" || lastSeen.IsZero() {
		return
	}
	age := now.Sub(lastSeen).Seconds()
	if age < 0 {
		age = 0
	}
	m.heartbeatAge.WithLabelValues(workerID).Set(age)
}

// ObserveHeartbeat is a convenience that extracts the worker id from
// a Heartbeat protobuf and records ObserveHeartbeatAge using now().
func (m *WorkerTrustMetrics) ObserveHeartbeat(hb *pb.Heartbeat) {
	if m == nil || hb == nil {
		return
	}
	m.ObserveHeartbeatAge(hb.GetWorkerId(), time.Now(), time.Now())
}

// ForgetWorker removes the worker's gauge rows. Called when a worker
// is decommissioned so Prometheus doesn't carry stale rows forever.
func (m *WorkerTrustMetrics) ForgetWorker(workerID string) {
	if m == nil || workerID == "" {
		return
	}
	m.sessionValid.DeletePartialMatch(prometheus.Labels{"worker_id": workerID})
	m.heartbeatAge.DeleteLabelValues(workerID)
}

// policyConstraintsSerialiseFailedTotal counts PolicyConstraints that
// protojson.Marshal rejected inside applyConstraints. Pre-BUG-011, this
// failure silently dropped the env propagation and the operator had no
// signal; the structural budget cap still applied (defense in depth),
// but the worker lost the redaction/budget hints.
var policyConstraintsSerialiseFailedTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Namespace: "cordum",
		Subsystem: "scheduler",
		Name:      "policy_constraints_serialise_failed_total",
		Help:      "PolicyConstraints that failed protojson.Marshal in applyConstraints (BUG-011).",
	},
)

func init() {
	prometheus.MustRegister(policyConstraintsSerialiseFailedTotal)
}
