package perfharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic contract fixture; not a measured workload result.
const workloadFixture = `name: fixture
description: Synthetic fixture
latency_scope: http
target: {base_url_env: PERF_URL, api_key_env: PERF_KEY, tenant_env: PERF_TENANT}
prerequisites: [A disposable target]
request_mix:
  - name: submit-approve
    weight: 1
    requests:
      - kind: jobs.submit
        payload: {prompt: '{{uuid}}-{{seq}}', topic: job.perf}
        capture: {job_id: job_id}
      - kind: jobs.approve
        path_params: {job_id: '{{capture.job_id}}'}
        payload: {}
profiles:
  smoke: {steps: [{concurrency: 1, duration: 1s}]}
  standard: {steps: [{concurrency: 2, duration: 2s}]}
saturation: {p99_ms: 1000, error_rate: 0.01, min_throughput_gain_pct: 5}
queue_depth: {kind: nats_jsz, url_env: PERF_QUEUE_URL, stream: CORDUM_JOBS, interval_ms: 100}
tolerance: {throughput_pct: 10, p99_pct: 10}
`

func loadFixture(t *testing.T, content string) (*Workload, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return LoadWorkload(path)
}

func TestWorkloadLoadValid(t *testing.T) {
	for _, content := range []string{workloadFixture, strings.ReplaceAll(workloadFixture, "\n", "\r\n")} {
		w, err := loadFixture(t, content)
		if err != nil {
			t.Fatal(err)
		}
		if w.Name != "fixture" || len(w.RequestMix) != 1 || len(w.RequestMix[0].Requests) != 2 {
			t.Fatalf("decoded workload: %+v", w)
		}
		if w.Profiles["smoke"].Steps[0].Concurrency != 1 {
			t.Fatal("smoke concurrency changed")
		}
	}
}

func TestWorkloadRejects(t *testing.T) {
	cases := []struct{ name, old, replacement, rule string }{
		{"filename", "name: fixture", "name: other", "name"},
		{"scope", "latency_scope: http", "latency_scope: terminal", "unsupported"},
		{"unknown field", "description:", "not_a_field:", "not_a_field"},
		{"unknown kind", "jobs.submit", "jobs.unknown", "kind"},
		{"weight", "weight: 1", "weight: 0", "weight"},
		{"fractional weight", "weight: 1", "weight: 1.5", "weight"},
		{"forward capture", "{{uuid}}-{{seq}}", "{{capture.job_id}}", "earlier"},
		{"missing capture", "{{capture.job_id}}", "{{capture.missing}}", "earlier"},
		{"unknown token", "{{uuid}}-{{seq}}", "{{password}}", "token"},
		{"unfinished token", "{{uuid}}-{{seq}}", "{{uuid}", "token"},
		{"smoke missing", "smoke:", "tiny:", "smoke"},
		{"smoke not smallest", "concurrency: 1, duration: 1s", "concurrency: 9, duration: 9s", "smallest"},
		{"zero concurrency", "concurrency: 1", "concurrency: 0", "concurrency"},
		{"zero duration", "duration: 1s", "duration: 0s", "duration"},
		{"invalid duration", "duration: 1s", "duration: later", "duration"},
		{"p99 threshold", "p99_ms: 1000", "p99_ms: 0", "p99_ms"},
		{"error rate zero", "error_rate: 0.01", "error_rate: 0", "error_rate"},
		{"error rate high", "error_rate: 0.01", "error_rate: 2", "error_rate"},
		{"gain", "min_throughput_gain_pct: 5", "min_throughput_gain_pct: -1", "min_throughput_gain_pct"},
		{"tolerance throughput", "throughput_pct: 10", "throughput_pct: 0", "throughput_pct"},
		{"tolerance p99", "p99_pct: 10", "p99_pct: 0", "p99_pct"},
		{"queue kind", "kind: nats_jsz", "kind: redis", "queue_depth"},
		{"queue stream", "stream: CORDUM_JOBS", "stream: ''", "stream"},
		{"queue metric", "kind: nats_jsz", "kind: prometheus", "metric"},
		{"queue interval", "interval_ms: 100", "interval_ms: 0", "interval_ms"},
		{"secret payload", "prompt:", "Password:", "secret"},
		{"nested secret", "topic: job.perf", "nested: [{ToKeN: forbidden}]", "secret"},
		{"secret path", "path_params: {job_id:", "path_params: {AUTHORIZATION:", "secret"},
		{"env value", "api_key_env: PERF_KEY", "api_key_env: secret-value", "api_key_env"},
		{"trailing yaml", "p99_pct: 10}", "p99_pct: 10}\n---\nname: second", "trailing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := loadFixture(t, strings.Replace(workloadFixture, tc.old, tc.replacement, 1))
			if w != nil || err == nil || !strings.Contains(err.Error(), tc.rule) || !strings.Contains(err.Error(), "fixture") {
				t.Fatalf("want fixture/%s rejection, got %v", tc.rule, err)
			}
		})
	}
}

func TestWorkloadEmptyCollections(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Workload)
		field  string
	}{
		{"empty mix", func(w *Workload) { w.RequestMix = nil }, "request_mix"},
		{"empty requests", func(w *Workload) { w.RequestMix[0].Requests = nil }, "requests"},
		{"empty profile", func(w *Workload) { w.Profiles["smoke"] = Profile{} }, "steps"},
		{"capture scope", func(w *Workload) {
			w.RequestMix = append(w.RequestMix, Sequence{Name: "second", Weight: 1, Requests: []Request{w.RequestMix[0].Requests[1]}})
		}, "earlier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := loadFixture(t, workloadFixture)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(w)
			if err := w.Validate(); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("want %s rejection, got %v", tc.field, err)
			}
		})
	}
}
