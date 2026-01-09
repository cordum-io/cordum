package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func withTestRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	origReg := prometheus.DefaultRegisterer
	origGather := prometheus.DefaultGatherer
	reg := prometheus.NewRegistry()
	prometheus.DefaultRegisterer = reg
	prometheus.DefaultGatherer = reg
	t.Cleanup(func() {
		prometheus.DefaultRegisterer = origReg
		prometheus.DefaultGatherer = origGather
	})
	return reg
}

func TestNoopMetrics(t *testing.T) {
	var m Noop
	m.IncJobsReceived("topic")
	m.IncJobsDispatched("topic")
	m.IncJobsCompleted("topic", "ok")
	m.IncSafetyDenied("topic")
}

func TestPromMetrics(t *testing.T) {
	reg := withTestRegistry(t)
	m := NewProm("cordum")
	m.IncJobsReceived("job.test")
	m.IncJobsDispatched("job.test")
	m.IncJobsCompleted("job.test", "ok")
	m.IncSafetyDenied("job.test")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if !hasMetric(families, "cordum_jobs_received_total", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected jobs_received metric")
	}
	if !hasMetric(families, "cordum_jobs_dispatched_total", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected jobs_dispatched metric")
	}
	if !hasMetric(families, "cordum_jobs_completed_total", map[string]string{"topic": "job.test", "status": "ok"}) {
		t.Fatalf("expected jobs_completed metric")
	}
	if !hasMetric(families, "cordum_safety_denied_total", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected safety_denied metric")
	}
}

func TestGatewayMetrics(t *testing.T) {
	reg := withTestRegistry(t)
	m := NewGatewayProm("cordum")
	m.ObserveRequest("GET", "/v1/health", "200", 0.01)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if !hasMetric(families, "cordum_http_requests_total", map[string]string{"method": "GET", "route": "/v1/health", "status": "200"}) {
		t.Fatalf("expected http_requests metric")
	}
	if !hasMetric(families, "cordum_http_request_duration_seconds", map[string]string{"method": "GET", "route": "/v1/health"}) {
		t.Fatalf("expected http_request_duration metric")
	}
}

func TestWorkflowMetrics(t *testing.T) {
	reg := withTestRegistry(t)
	m := NewWorkflowProm("cordum")
	m.IncWorkflowStarted("wf")
	m.IncWorkflowCompleted("wf", "ok")
	m.ObserveWorkflowDuration("wf", 0.5)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if !hasMetric(families, "cordum_workflows_started_total", map[string]string{"workflow": "wf"}) {
		t.Fatalf("expected workflows_started metric")
	}
	if !hasMetric(families, "cordum_workflows_completed_total", map[string]string{"workflow": "wf", "status": "ok"}) {
		t.Fatalf("expected workflows_completed metric")
	}
	if !hasMetric(families, "cordum_workflow_duration_seconds", map[string]string{"workflow": "wf"}) {
		t.Fatalf("expected workflow_duration metric")
	}
}

func TestHandler(t *testing.T) {
	withTestRegistry(t)
	m := NewProm("cordum")
	m.IncJobsReceived("job.test")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("expected metrics output")
	}
}

func hasMetric(families []*dto.MetricFamily, name string, labels map[string]string) bool {
	for _, fam := range families {
		if fam.GetName() != name {
			continue
		}
		for _, metric := range fam.GetMetric() {
			if matchLabels(metric.GetLabel(), labels) {
				return true
			}
		}
	}
	return false
}

func matchLabels(pairs []*dto.LabelPair, labels map[string]string) bool {
	if len(labels) == 0 {
		return true
	}
	found := 0
	for _, pair := range pairs {
		if val, ok := labels[pair.GetName()]; ok && pair.GetValue() == val {
			found++
		}
	}
	return found == len(labels)
}
