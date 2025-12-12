package metrics

import (
	"net/http"
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

func (Noop) IncJobsReceived(string)          {}
func (Noop) IncJobsDispatched(string)        {}
func (Noop) IncJobsCompleted(string, string) {}
func (Noop) IncSafetyDenied(string)          {}

// Prom implements Metrics backed by Prometheus counters.
type Prom struct {
	jobsReceived   *prometheus.CounterVec
	jobsDispatched *prometheus.CounterVec
	jobsCompleted  *prometheus.CounterVec
	safetyDenied   *prometheus.CounterVec
	once           sync.Once
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
	}
	p.register()
	return p
}

func (p *Prom) register() {
	p.once.Do(func() {
		prometheus.MustRegister(p.jobsReceived, p.jobsDispatched, p.jobsCompleted, p.safetyDenied)
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

// Handler returns an HTTP handler for /metrics.
func Handler() http.Handler {
	return promhttp.Handler()
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
