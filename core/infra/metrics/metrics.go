package metrics

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics defines counters for scheduler and workers.
type Metrics interface {
	IncJobsReceived(topic string)
	IncJobsDispatched(topic string)
	IncJobsCompleted(topic, status string)
	IncSafetyDenied(topic string)
	IncSafetyUnavailable(topic string)
	IncOrphanReplayed(topic string)
	ObserveJobLockWait(seconds float64)
	ObserveDispatchLatency(topic string, seconds float64)
	SetActiveGoroutines(count int)
	SetStaleJobs(state string, count int)
	IncSagaRecorded()
	IncSagaRollbackTriggered()
	IncSagaCompensationDispatched()
	IncSagaCompensationFailed()
	ObserveSagaRollback(durationSeconds float64)
	IncSagaActive()
	DecSagaActive()
	IncSagaUnmarshalError()
}

// GatewayMetrics captures request metrics for the API gateway.
type GatewayMetrics interface {
	ObserveRequest(method, route, status string, durationSeconds float64)
}

// WorkflowMetrics captures orchestrator-level workflow metrics.
type WorkflowMetrics interface {
	IncWorkflowStarted(workflow string)
	IncWorkflowCompleted(workflow, status string)
	ObserveWorkflowDuration(workflow string, durationSeconds float64)
}

// Noop implements Metrics without emitting anything.
type Noop struct{}

func (Noop) IncJobsReceived(string)                {}
func (Noop) IncJobsDispatched(string)              {}
func (Noop) IncJobsCompleted(string, string)       {}
func (Noop) IncSafetyDenied(string)                {}
func (Noop) IncSafetyUnavailable(string)           {}
func (Noop) IncOrphanReplayed(string)              {}
func (Noop) ObserveJobLockWait(float64)            {}
func (Noop) ObserveDispatchLatency(string, float64) {}
func (Noop) SetActiveGoroutines(int)               {}
func (Noop) SetStaleJobs(string, int)              {}
func (Noop) IncSagaRecorded()                      {}
func (Noop) IncSagaRollbackTriggered()       {}
func (Noop) IncSagaCompensationDispatched()  {}
func (Noop) IncSagaCompensationFailed()      {}
func (Noop) ObserveSagaRollback(float64)     {}
func (Noop) IncSagaActive()                  {}
func (Noop) DecSagaActive()                  {}
func (Noop) IncSagaUnmarshalError()          {}

// Prom implements Metrics backed by Prometheus counters.
type Prom struct {
	jobsReceived       *prometheus.CounterVec
	jobsDispatched     *prometheus.CounterVec
	jobsCompleted      *prometheus.CounterVec
	safetyDenied       *prometheus.CounterVec
	safetyUnavailable  *prometheus.CounterVec
	orphanReplayed     *prometheus.CounterVec
	jobLockWait        prometheus.Histogram
	dispatchLatency    *prometheus.HistogramVec
	activeGoroutines   prometheus.Gauge
	staleJobs          *prometheus.GaugeVec
	sagaRecorded       prometheus.Counter
	sagaRollbacks      prometheus.Counter
	sagaDispatched     prometheus.Counter
	sagaFailed         prometheus.Counter
	sagaActive         prometheus.Gauge
	sagaDuration       prometheus.Histogram
	sagaUnmarshalErrors prometheus.Counter
	once               sync.Once
}

func NewProm(namespace string) *Prom {
	p := &Prom{
		jobsReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "jobs_received_total",
			Help:      "Jobs received by topic",
		}, []string{"topic"}),
		jobsDispatched: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "jobs_dispatched_total",
			Help:      "Jobs dispatched by topic",
		}, []string{"topic"}),
		jobsCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "jobs_completed_total",
			Help:      "Jobs completed by topic and status",
		}, []string{"topic", "status"}),
		safetyDenied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "safety_denied_total",
			Help:      "Jobs denied by safety kernel per topic",
		}, []string{"topic"}),
		safetyUnavailable: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "safety_unavailable_total",
			Help:      "Jobs deferred due to safety kernel unavailability per topic",
		}, []string{"topic"}),
		orphanReplayed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_orphan_replayed_total",
			Help:      "Orphaned PENDING jobs replayed by the pending replayer",
		}, []string{"topic"}),
		jobLockWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "scheduler_job_lock_wait_seconds",
			Help:      "Time spent waiting to acquire per-job lock",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}),
		dispatchLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "scheduler_dispatch_latency_seconds",
			Help:      "Latency from receive to dispatch per topic",
			Buckets:   prometheus.DefBuckets,
		}, []string{"topic"}),
		activeGoroutines: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "scheduler_active_goroutines",
			Help:      "Number of active handler goroutines in the scheduler",
		}),
		staleJobs: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "scheduler_stale_jobs",
			Help:      "Number of stale jobs detected by reconciler per state",
		}, []string{"state"}),
		sagaRecorded: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "saga_recorded_total",
			Help:      "Compensation steps recorded for sagas",
		}),
		sagaRollbacks: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "saga_rollbacks_total",
			Help:      "Saga rollbacks triggered",
		}),
		sagaDispatched: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "saga_compensation_dispatched_total",
			Help:      "Compensation jobs dispatched",
		}),
		sagaFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "saga_compensation_failed_total",
			Help:      "Compensation dispatch failures",
		}),
		sagaActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "saga_active",
			Help:      "Active saga rollbacks in progress",
		}),
		sagaDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "saga_rollback_duration_seconds",
			Help:      "Saga rollback duration in seconds",
			Buckets:   prometheus.DefBuckets,
		}),
		sagaUnmarshalErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "saga_unmarshal_errors_total",
			Help:      "Saga compensation entries that failed protobuf unmarshal",
		}),
	}
	p.register()
	return p
}

func (p *Prom) register() {
	p.once.Do(func() {
		prometheus.MustRegister(
			p.jobsReceived,
			p.jobsDispatched,
			p.jobsCompleted,
			p.safetyDenied,
			p.safetyUnavailable,
			p.orphanReplayed,
			p.jobLockWait,
			p.dispatchLatency,
			p.activeGoroutines,
			p.staleJobs,
			p.sagaRecorded,
			p.sagaRollbacks,
			p.sagaDispatched,
			p.sagaFailed,
			p.sagaActive,
			p.sagaDuration,
			p.sagaUnmarshalErrors,
		)
	})
}

func (p *Prom) IncJobsReceived(topic string) {
	p.jobsReceived.WithLabelValues(topic).Inc()
}

func (p *Prom) IncJobsDispatched(topic string) {
	p.jobsDispatched.WithLabelValues(topic).Inc()
}

func (p *Prom) IncJobsCompleted(topic, status string) {
	p.jobsCompleted.WithLabelValues(topic, status).Inc()
}

func (p *Prom) IncSafetyDenied(topic string) {
	p.safetyDenied.WithLabelValues(topic).Inc()
}

func (p *Prom) IncSafetyUnavailable(topic string) {
	p.safetyUnavailable.WithLabelValues(topic).Inc()
}

func (p *Prom) IncOrphanReplayed(topic string) {
	p.orphanReplayed.WithLabelValues(topic).Inc()
}

func (p *Prom) ObserveJobLockWait(seconds float64) {
	if seconds >= 0 {
		p.jobLockWait.Observe(seconds)
	}
}

func (p *Prom) ObserveDispatchLatency(topic string, seconds float64) {
	if seconds >= 0 {
		p.dispatchLatency.WithLabelValues(topic).Observe(seconds)
	}
}

func (p *Prom) SetActiveGoroutines(count int) {
	p.activeGoroutines.Set(float64(count))
}

func (p *Prom) SetStaleJobs(state string, count int) {
	p.staleJobs.WithLabelValues(state).Set(float64(count))
}

func (p *Prom) IncSagaRecorded() {
	p.sagaRecorded.Inc()
}

func (p *Prom) IncSagaRollbackTriggered() {
	p.sagaRollbacks.Inc()
}

func (p *Prom) IncSagaCompensationDispatched() {
	p.sagaDispatched.Inc()
}

func (p *Prom) IncSagaCompensationFailed() {
	p.sagaFailed.Inc()
}

func (p *Prom) ObserveSagaRollback(durationSeconds float64) {
	if durationSeconds >= 0 {
		p.sagaDuration.Observe(durationSeconds)
	}
}

func (p *Prom) IncSagaActive() {
	p.sagaActive.Inc()
}

func (p *Prom) DecSagaActive() {
	p.sagaActive.Dec()
}

func (p *Prom) IncSagaUnmarshalError() {
	p.sagaUnmarshalErrors.Inc()
}

// Handler returns an HTTP handler for /metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}

// ValidateBindAddr rejects public binds unless explicitly allowed.
func ValidateBindAddr(addr string, allowPublic bool) error {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return fmt.Errorf("metrics addr required")
	}
	if allowPublic {
		return nil
	}
	if isPublicBindAddr(trimmed) {
		return fmt.Errorf("public metrics bind %q requires explicit allow", trimmed)
	}
	return nil
}

func isPublicBindAddr(addr string) bool {
	host := addr
	if strings.HasPrefix(host, ":") {
		host = ""
	} else if strings.Contains(host, ":") {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.Trim(host, "[]")
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback()
	}
	return true
}

// --- Gateway metrics ---

type gatewayProm struct {
	requests *prometheus.CounterVec
	latency  *prometheus.HistogramVec
	once     sync.Once
}

// NewGatewayProm constructs a GatewayMetrics with counters/histograms.
func NewGatewayProm(namespace string) GatewayMetrics {
	g := &gatewayProm{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_requests_total",
			Help:      "HTTP requests by method/route/status",
		}, []string{"method", "route", "status"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency by method/route",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
	g.once.Do(func() {
		prometheus.MustRegister(g.requests, g.latency)
	})
	return g
}

func (g *gatewayProm) ObserveRequest(method, route, status string, durationSeconds float64) {
	g.requests.WithLabelValues(method, route, status).Inc()
	g.latency.WithLabelValues(method, route).Observe(durationSeconds)
}

// --- Workflow metrics (orchestrator) ---

type workflowProm struct {
	started   *prometheus.CounterVec
	completed *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	once      sync.Once
}

func NewWorkflowProm(namespace string) WorkflowMetrics {
	w := &workflowProm{
		started: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "workflows_started_total",
			Help:      "Workflows started by name",
		}, []string{"workflow"}),
		completed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "workflows_completed_total",
			Help:      "Workflows completed by name and status",
		}, []string{"workflow", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "workflow_duration_seconds",
			Help:      "Workflow duration seconds by name",
			Buckets:   prometheus.DefBuckets,
		}, []string{"workflow"}),
	}
	w.once.Do(func() {
		prometheus.MustRegister(w.started, w.completed, w.duration)
	})
	return w
}

func (w *workflowProm) IncWorkflowStarted(workflow string) {
	w.started.WithLabelValues(workflow).Inc()
}

func (w *workflowProm) IncWorkflowCompleted(workflow, status string) {
	w.completed.WithLabelValues(workflow, status).Inc()
}

func (w *workflowProm) ObserveWorkflowDuration(workflow string, durationSeconds float64) {
	w.duration.WithLabelValues(workflow).Observe(durationSeconds)
}
