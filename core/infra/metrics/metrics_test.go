package metrics

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func withTestRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	origReg := prometheus.DefaultRegisterer
	origGather := prometheus.DefaultGatherer
	origPodReg := podRegisterer
	reg := prometheus.NewRegistry()
	prometheus.DefaultRegisterer = reg
	prometheus.DefaultGatherer = reg
	podRegisterer = prometheus.WrapRegistererWith(
		prometheus.Labels{"pod": resolvePodName()},
		reg,
	)
	t.Cleanup(func() {
		prometheus.DefaultRegisterer = origReg
		prometheus.DefaultGatherer = origGather
		podRegisterer = origPodReg
	})
	return reg
}

func TestNoopMetrics(t *testing.T) {
	var m Noop
	m.IncJobsReceived("topic")
	m.IncJobsDispatched("topic")
	m.IncJobsCompleted("topic", "ok")
	m.IncSafetyDenied("topic")
	m.IncOutputPolicyChecked("topic")
	m.IncOutputPolicyQuarantined("topic")
	m.IncOutputPolicySkipped("topic")
	m.IncAsyncOutputTimeout("topic")
	m.IncOutputEvaluations("topic")
	m.IncOutputDenials("topic")
	m.IncOutputRedactions("topic")
	m.ObserveJobLockWait(0.01)
	m.ObserveDispatchLatency("topic", 0.02)
	m.ObserveOutputCheckLatency("topic", "sync", 0.01)
	m.ObserveOutputEvalDuration("topic", 0.005)
	m.SetActiveGoroutines(5)
	m.SetStaleJobs("DISPATCHED", 2)
	m.IncSagaRecorded()
	m.IncSagaRollbackTriggered()
	m.IncSagaCompensationDispatched()
	m.IncSagaCompensationFailed()
	m.ObserveSagaRollback(0.1)
	m.IncSagaActive()
	m.DecSagaActive()
	m.IncJobCancelFailures()
	m.IncValidationRejections()
}

func TestNoopApprovalMetrics(t *testing.T) {
	var m NoopApproval
	m.SetApprovalQueueDepth("all", 1)
	m.ObserveApprovalLatency(0.25)
	m.IncApprovalExpired()
	m.IncApprovalDecision("approved")
}

func TestPromMetrics(t *testing.T) {
	reg := withTestRegistry(t)
	m := NewProm("cordum")
	m.IncJobsReceived("job.test")
	m.IncJobsDispatched("job.test")
	m.IncJobsCompleted("job.test", "ok")
	m.IncSafetyDenied("job.test")
	m.IncOutputPolicyChecked("job.test")
	m.IncOutputPolicyQuarantined("job.test")
	m.IncOutputPolicySkipped("job.test")
	m.IncAsyncOutputTimeout("job.test")
	m.IncOutputEvaluations("job.test")
	m.IncOutputDenials("job.test")
	m.IncOutputRedactions("job.test")
	m.ObserveJobLockWait(0.01)
	m.ObserveDispatchLatency("job.test", 0.02)
	m.ObserveOutputCheckLatency("job.test", "sync", 0.01)
	m.ObserveOutputEvalDuration("job.test", 0.005)
	m.SetActiveGoroutines(3)
	m.SetStaleJobs("DISPATCHED", 1)
	m.IncSagaRecorded()
	m.IncSagaRollbackTriggered()
	m.IncSagaCompensationDispatched()
	m.IncSagaCompensationFailed()
	m.ObserveSagaRollback(0.05)
	m.IncSagaActive()
	m.DecSagaActive()
	m.IncJobCancelFailures()
	m.IncValidationRejections()

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
	if !hasMetric(families, "cordum_output_policy_checked_total", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected output_policy_checked metric")
	}
	if !hasMetric(families, "cordum_output_policy_quarantined_total", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected output_policy_quarantined metric")
	}
	if !hasMetric(families, "cordum_output_policy_skipped_total", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected output_policy_skipped metric")
	}
	if !hasMetric(families, "cordum_output_evaluations_total", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected output_evaluations metric")
	}
	if !hasMetric(families, "cordum_output_denials_total", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected output_denials metric")
	}
	if !hasMetric(families, "cordum_output_redactions_total", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected output_redactions metric")
	}
	if !hasMetric(families, "cordum_scheduler_job_lock_wait_seconds", nil) {
		t.Fatalf("expected job_lock_wait metric")
	}
	if !hasMetric(families, "cordum_scheduler_dispatch_latency_seconds", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected dispatch_latency metric")
	}
	if !hasMetric(families, "cordum_output_check_latency_seconds", map[string]string{"topic": "job.test", "phase": "sync"}) {
		t.Fatalf("expected output_check_latency metric")
	}
	if !hasMetric(families, "cordum_output_eval_duration_seconds", map[string]string{"topic": "job.test"}) {
		t.Fatalf("expected output_eval_duration metric")
	}
	if !hasMetric(families, "cordum_scheduler_active_goroutines", nil) {
		t.Fatalf("expected active_goroutines metric")
	}
	if !hasMetric(families, "cordum_scheduler_stale_jobs", map[string]string{"state": "DISPATCHED"}) {
		t.Fatalf("expected stale_jobs metric")
	}
	if !hasMetric(families, "cordum_saga_recorded_total", nil) {
		t.Fatalf("expected saga_recorded metric")
	}
	if !hasMetric(families, "cordum_saga_rollbacks_total", nil) {
		t.Fatalf("expected saga_rollbacks metric")
	}
	if !hasMetric(families, "cordum_saga_compensation_dispatched_total", nil) {
		t.Fatalf("expected saga_dispatched metric")
	}
	if !hasMetric(families, "cordum_saga_compensation_failed_total", nil) {
		t.Fatalf("expected saga_failed metric")
	}
	if !hasMetric(families, "cordum_saga_active", nil) {
		t.Fatalf("expected saga_active metric")
	}
	if !hasMetric(families, "cordum_saga_rollback_duration_seconds", nil) {
		t.Fatalf("expected saga_rollback_duration metric")
	}
	if !hasMetric(families, "cordum_job_cancel_failures_total", nil) {
		t.Fatalf("expected job_cancel_failures metric")
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

func TestApprovalMetrics(t *testing.T) {
	reg := withTestRegistry(t)
	m := NewApprovalProm("cordum")
	m.SetApprovalQueueDepth("all", 3)
	m.ObserveApprovalLatency(0.5)
	m.IncApprovalExpired()
	m.IncApprovalDecision("approved")
	m.IncApprovalDecision("denied")
	m.IncApprovalDecision("expired")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if !hasMetric(families, "cordum_approval_queue_depth", map[string]string{"risk_tier": "all"}) {
		t.Fatalf("expected approval_queue_depth metric")
	}
	if !hasMetric(families, "cordum_approval_latency_seconds", nil) {
		t.Fatalf("expected approval_latency_seconds metric")
	}
	if !hasMetric(families, "cordum_approval_expired_total", nil) {
		t.Fatalf("expected approval_expired_total metric")
	}
	if !hasMetric(families, "cordum_approval_decisions_total", map[string]string{"decision": "approved"}) {
		t.Fatalf("expected approval_decisions_total approved metric")
	}
	if !hasMetric(families, "cordum_approval_decisions_total", map[string]string{"decision": "denied"}) {
		t.Fatalf("expected approval_decisions_total denied metric")
	}
	if !hasMetric(families, "cordum_approval_decisions_total", map[string]string{"decision": "expired"}) {
		t.Fatalf("expected approval_decisions_total expired metric")
	}
}

func TestHandler(t *testing.T) {
	withTestRegistry(t)
	m := NewProm("cordum")
	m.IncJobsReceived("job.test")
	a := NewApprovalProm("cordum")
	a.SetApprovalQueueDepth("all", 1)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("expected metrics output")
	}
	if !strings.Contains(rec.Body.String(), "cordum_approval_queue_depth") {
		t.Fatalf("expected approval metrics in handler output")
	}
}

func TestValidateBindAddr(t *testing.T) {
	cases := []struct {
		name        string
		addr        string
		allowPublic bool
		wantErr     bool
	}{
		{name: "empty", addr: "", allowPublic: false, wantErr: true},
		{name: "loopback ipv4", addr: "127.0.0.1:9090", allowPublic: false, wantErr: false},
		{name: "loopback ipv6", addr: "[::1]:9090", allowPublic: false, wantErr: false},
		{name: "localhost", addr: "localhost:9090", allowPublic: false, wantErr: false},
		{name: "wildcard", addr: ":9090", allowPublic: false, wantErr: true},
		{name: "public ip", addr: "192.168.0.1:9090", allowPublic: false, wantErr: true},
		{name: "allow public", addr: ":9090", allowPublic: true, wantErr: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBindAddr(tc.addr, tc.allowPublic)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
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

func TestMetricsPodLabel(t *testing.T) {
	reg := withTestRegistry(t)
	m := NewProm("cordum")
	m.IncJobsReceived("job.pod-test")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Every metric family should have a "pod" label on its metrics.
	podName := resolvePodName()
	for _, fam := range families {
		for _, metric := range fam.GetMetric() {
			var found bool
			for _, lp := range metric.GetLabel() {
				if lp.GetName() == "pod" {
					found = true
					if lp.GetValue() != podName {
						t.Fatalf("metric %s: expected pod=%q, got %q", fam.GetName(), podName, lp.GetValue())
					}
					break
				}
			}
			if !found {
				t.Fatalf("metric %s missing pod label", fam.GetName())
			}
		}
	}
}

func TestMetricsPodLabelGateway(t *testing.T) {
	reg := withTestRegistry(t)
	m := NewGatewayProm("cordum")
	m.ObserveRequest("GET", "/health", "200", 0.01)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	podName := resolvePodName()
	for _, fam := range families {
		for _, metric := range fam.GetMetric() {
			if !hasLabel(metric.GetLabel(), "pod", podName) {
				t.Fatalf("gateway metric %s missing pod=%s label", fam.GetName(), podName)
			}
		}
	}
}

func TestMetricsPodLabelWorkflow(t *testing.T) {
	reg := withTestRegistry(t)
	m := NewWorkflowProm("cordum")
	m.IncWorkflowStarted("wf-test")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	podName := resolvePodName()
	for _, fam := range families {
		for _, metric := range fam.GetMetric() {
			if !hasLabel(metric.GetLabel(), "pod", podName) {
				t.Fatalf("workflow metric %s missing pod=%s label", fam.GetName(), podName)
			}
		}
	}
}

func TestMetricsPodLabelApproval(t *testing.T) {
	reg := withTestRegistry(t)
	m := NewApprovalProm("cordum")
	m.SetApprovalQueueDepth("all", 1)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	podName := resolvePodName()
	for _, fam := range families {
		for _, metric := range fam.GetMetric() {
			if !hasLabel(metric.GetLabel(), "pod", podName) {
				t.Fatalf("approval metric %s missing pod=%s label", fam.GetName(), podName)
			}
		}
	}
}

func TestResolvePodNameEnvOverride(t *testing.T) {
	t.Setenv("CORDUM_INSTANCE_ID", "custom-pod-42")
	got := resolvePodName()
	if got != "custom-pod-42" {
		t.Fatalf("expected custom-pod-42, got %s", got)
	}
}

func TestResolvePodNameHostnameFallback(t *testing.T) {
	t.Setenv("CORDUM_INSTANCE_ID", "")
	got := resolvePodName()
	hostname, _ := os.Hostname()
	if hostname != "" && got != hostname {
		t.Fatalf("expected hostname %q, got %q", hostname, got)
	}
	if got == "" {
		t.Fatalf("expected non-empty pod name")
	}
}

func hasLabel(pairs []*dto.LabelPair, name, value string) bool {
	for _, lp := range pairs {
		if lp.GetName() == name && lp.GetValue() == value {
			return true
		}
	}
	return false
}
