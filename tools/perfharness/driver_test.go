package perfharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const approveRequest = `      - kind: jobs.approve
        path_params: {job_id: '{{capture.job_id}}'}
        payload: {}
`

const approverRefusal = "jobs.approve needs CORDUM_APPROVER_API_KEY set to a credential distinct from the submit key (the gateway denies self-approval)"

func setTarget(t *testing.T, url, key, approver string) {
	t.Helper()
	t.Setenv("PERF_URL", url)
	t.Setenv("PERF_KEY", key)
	t.Setenv(approverKeyEnv, approver)
	t.Setenv("PERF_TENANT", "tenant")
	t.Setenv("CORDUM_TLS_CA", "")
}

func writeFixture(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Error(err)
	}
}

func TestCLIValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	data, err := WriteResults(syntheticResults(t))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		args    []string
		success bool
	}{
		{"valid", []string{"validate", path}, true},
		{"missing", []string{"validate", path + "missing"}, false},
		{"arguments", []string{"run"}, false},
		{"unknown", []string{"oops"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			args := append([]string{"run", "./cmd/perfharness"}, tc.args...)
			output, err := exec.CommandContext(ctx, "go", args...).CombinedOutput()
			if (err == nil) != tc.success || !tc.success && len(output) == 0 {
				t.Fatalf("err=%v output=%s", err, output)
			}
		})
	}
}

func TestRequestAdapters(t *testing.T) {
	const key, approver = "distinctive-key-must-never-be-recorded", "distinctive-approver-never-recorded"
	keys := map[string]string{"/api/v1/status": key, "/api/v1/jobs": key, "/api/v1/jobs/job-1": approver, "/api/v1/approvals/job-1/approve": approver}
	var polls, approvals atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != keys[r.URL.Path] || r.Header.Get("X-Tenant-ID") != "tenant" {
			t.Errorf("%s: wrong credential or tenant", r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/v1/status":
			writeFixture(t, w, `{"build":{"version":"v-test"}}`)
		case "/api/v1/jobs":
			var body struct {
				Prompt string `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if strings.Contains(body.Prompt, "{{") || !strings.HasSuffix(body.Prompt, "-1") {
				t.Errorf("prompt %s", body.Prompt)
			}
			writeFixture(t, w, `{"job_id":"job-1"}`)
		case "/api/v1/jobs/job-1":
			if polls.Add(1) == 1 {
				writeFixture(t, w, `{"state":"PENDING"}`)
			} else {
				writeFixture(t, w, `{"state":"APPROVAL_REQUIRED"}`)
			}
		case "/api/v1/approvals/job-1/approve":
			if polls.Load() < 2 {
				t.Error("premature approval")
			}
			approvals.Add(1)
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	w, err := loadFixture(t, workloadFixture)
	if err != nil {
		t.Fatal(err)
	}
	setTarget(t, server.URL, key, approver)
	e, err := newExecutor(w)
	if err != nil {
		t.Fatal(err)
	}
	defer e.client.CloseIdleConnections()
	if version := e.version(context.Background()); version != (TargetVersion{Version: "v-test", Source: "status_endpoint"}) {
		t.Fatalf("version %+v", version)
	}
	var count int
	e.execute(context.Background(), func(_ time.Duration, err error) {
		count++
		if err != nil {
			t.Error(err)
		}
	})
	if count != 2 || approvals.Load() != 1 {
		t.Fatalf("samples=%d approvals=%d", count, approvals.Load())
	}
	r := syntheticResults(t)
	r.Environment.Versions.Target = e.version(context.Background())
	data, err := WriteResults(r)
	if err != nil || bytes.Contains(data, []byte(key)) || bytes.Contains(data, []byte(approver)) {
		t.Fatal("key leaked or invalid results")
	}
}

func TestApproverKeyContract(t *testing.T) {
	submitOnly := strings.Replace(workloadFixture, approveRequest, "", 1)
	for _, tc := range []struct{ name, workload, approver, err string }{
		{"unset", workloadFixture, "", approverRefusal},
		{"same as submit key", workloadFixture, "submit-key", approverRefusal},
		{"distinct", workloadFixture, "approver-key", ""},
		{"no jobs.approve never reads it", submitOnly, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, err := loadFixture(t, tc.workload)
			if err != nil {
				t.Fatal(err)
			}
			setTarget(t, "http://localhost:8081", "submit-key", tc.approver)
			e, err := newExecutor(w)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			e.client.CloseIdleConnections()
		})
	}
}

func TestApprovalBoundAndUnknownVersion(t *testing.T) {
	var approvals atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "approve") {
			approvals.Add(1)
		}
		writeFixture(t, w, `{"state":"PENDING"}`)
	}))
	defer server.Close()
	w, err := loadFixture(t, workloadFixture)
	if err != nil {
		t.Fatal(err)
	}
	setTarget(t, server.URL, "key", "approver")
	e, err := newExecutor(w)
	if err != nil {
		t.Fatal(err)
	}
	defer e.client.CloseIdleConnections()
	if got := e.version(context.Background()); got != (TargetVersion{Version: "unknown", Source: "unknown"}) {
		t.Fatalf("%+v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := e.awaitApproval(ctx, "job"); err == nil {
		t.Fatal("pending accepted")
	}
	if approvals.Load() != 0 {
		t.Fatal("approval mutation happened")
	}
}

func TestWeightedSequences(t *testing.T) {
	e := &executor{mix: []Sequence{{Name: "a", Weight: 1}, {Name: "b", Weight: 2}}, weight: 3}
	for n, want := range []string{"a", "b", "b", "a"} {
		if got := e.pick(uint64(n)).Name; got != want {
			t.Fatalf("%d: %s != %s", n, got, want)
		}
	}
}

func TestRunWritesMeasuredResults(t *testing.T) {
	const key, approver = "sentinel-secret-never-in-results", "sentinel-approver-never-in-results"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			writeFixture(t, w, `{"build":{"version":"test"}}`)
		case "/api/v1/jobs":
			writeFixture(t, w, `{"job_id":"job-1"}`)
		case "/api/v1/jobs/job-1":
			writeFixture(t, w, `{"state":"APPROVAL_REQUIRED"}`)
		case "/api/v1/approvals/job-1/approve":
			w.WriteHeader(204)
		default:
			writeFixture(t, w, `{"account_details":[{"stream_detail":[{"name":"CORDUM_JOBS","state":{"messages":7}}]}]}`)
		}
	}))
	defer server.Close()
	setTarget(t, server.URL, key, approver)
	t.Setenv("PERF_QUEUE_URL", server.URL+"/jsz")
	path := filepath.Join(t.TempDir(), "fixture.yaml")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(workloadFixture, "duration: 1s", "duration: 100ms")), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := Run(context.Background(), path, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseResults(data)
	if err != nil || bytes.Contains(data, []byte(key)) || bytes.Contains(data, []byte(approver)) {
		t.Fatal("invalid or secret-bearing results")
	}
	if len(r.Steps) != 1 || r.Steps[0].Requests == 0 || len(r.QueueDepth.Samples) == 0 || r.QueueDepth.Samples[0].Value == nil || *r.QueueDepth.Samples[0].Value != 7 {
		t.Fatalf("incomplete results: %+v", r)
	}
}

func TestDriverRecordedConfiguration(t *testing.T) {
	t.Setenv("PERF_URL", "http://localhost:8081")
	for _, tc := range []struct {
		workload string
		envVars  []string
	}{
		{workloadFixture, []string{"CORDUM_APPROVER_API_KEY", "CORDUM_TLS_CA", "PERF_KEY", "PERF_QUEUE_URL", "PERF_TENANT", "PERF_URL"}},
		{strings.Replace(workloadFixture, approveRequest, "", 1), []string{"CORDUM_TLS_CA", "PERF_KEY", "PERF_QUEUE_URL", "PERF_TENANT", "PERF_URL"}},
	} {
		path := filepath.Join(t.TempDir(), "fixture.yaml")
		if err := os.WriteFile(path, []byte(tc.workload), 0600); err != nil {
			t.Fatal(err)
		}
		w, r, err := prepareResults(context.Background(), path, "smoke")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r.Environment.Configuration.RequestMix, w.RequestMix) || !reflect.DeepEqual(r.Environment.Configuration.ProfileSteps, w.Profiles["smoke"].Steps) {
			t.Fatal("configuration changed")
		}
		if !reflect.DeepEqual(r.Environment.Configuration.EnvVarsRead, tc.envVars) {
			t.Fatalf("env_vars_read %v", r.Environment.Configuration.EnvVarsRead)
		}
		r.Steps = []StepResult{{Index: 0, Concurrency: 1, DurationMS: 1000}}
		r.Saturation = findSaturation(r.Steps, w.Saturation)
		if _, err := WriteResults(*r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDriverStatistics(t *testing.T) {
	samples := []float64{100, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	got := summarizeStep(samples, 12, 2, time.Second)
	want := StepResult{DurationMS: 1000, Requests: 12, Errors: 2, ThroughputRPS: 10,
		LatencyMS: Latency{P50: 5, P90: 9, P95: 100, P99: 100, Max: 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v; want %+v", got, want)
	}
	if got := summarizeStep(nil, 3, 3, time.Second); got.LatencyMS != (Latency{}) || got.ThroughputRPS != 0 {
		t.Fatalf("all errors: %+v", got)
	}
}

func TestDriverSaturation(t *testing.T) {
	thresholds := Thresholds{P99MS: 100, ErrorRate: .1, MinThroughputGainPct: 5}
	for _, tc := range []struct {
		name  string
		steps []StepResult
		rule  string
		index int
	}{
		{"latency", []StepResult{{Requests: 10, LatencyMS: Latency{P99: 101}}, {Requests: 10, Errors: 9}}, "p99_ms", 0},
		{"errors", []StepResult{{Requests: 10, Errors: 2}}, "error_rate", 0},
		{"gain", []StepResult{{ThroughputRPS: 100}, {ThroughputRPS: 104}}, "min_throughput_gain_pct", 1},
		{"equality", []StepResult{{Requests: 10, Errors: 1, LatencyMS: Latency{P99: 100}, ThroughputRPS: 100}, {ThroughputRPS: 105}}, "not reached", -1},
		{"zero previous", []StepResult{{}, {ThroughputRPS: 10}}, "not reached", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := findSaturation(tc.steps, thresholds)
			if got.Rule != tc.rule || got.Reached != (tc.index >= 0) {
				t.Fatalf("%+v", got)
			}
			if tc.index >= 0 && (got.StepIndex == nil || *got.StepIndex != tc.index) {
				t.Fatalf("index: %+v", got)
			}
			if tc.index < 0 && (got.StepIndex != nil || got.Reason != "not reached; highest step 1 (concurrency 0)") {
				t.Fatalf("not reached: %+v", got)
			}
		})
	}
}

func TestDriverBudgetBoundary(t *testing.T) {
	p := Profile{Steps: []ProfileStep{{Concurrency: 1, Duration: "1s"}}}
	// 1s + 2s teardown at 100ms, plus the initial sample: 31 * 160 + 1024.
	if err := checkRunBudget(p, 100, 3139744); err != nil {
		t.Fatal(err)
	}
	if err := checkRunBudget(p, 100, 3139745); err == nil {
		t.Fatal("over-budget run admitted")
	}
}

func TestDriverBudgetRejectsOverflow(t *testing.T) {
	for _, tc := range []struct {
		duration    string
		interval    Milliseconds
		concurrency Concurrency
	}{
		{"2562047h", 9223372036854, 1}, {"1s", 9223372036855, 1}, {"1s", 100, 4097},
	} {
		p := Profile{Steps: []ProfileStep{{Duration: tc.duration, Concurrency: tc.concurrency}, {Duration: tc.duration, Concurrency: tc.concurrency}}}
		if err := checkRunBudget(p, tc.interval, 0); err == nil {
			t.Fatalf("unsafe budget admitted: %+v", tc)
		}
	}
}

func TestDriverHTTPAndCancellation(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n > 4 {
			<-r.Context().Done()
			return
		}
		time.Sleep(2 * time.Millisecond)
		if n == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := server.Client()
	got, err := runStep(context.Background(), ProfileStep{Concurrency: 1, Duration: "100ms"}, 0,
		func(ctx context.Context, record func(time.Duration, error)) {
			req, e := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			if e != nil {
				t.Error(e)
				return
			}
			start := time.Now()
			res, e := client.Do(req)
			if e == nil {
				if res.StatusCode != 204 {
					e = fmt.Errorf("HTTP status")
				}
				if closeErr := res.Body.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			record(time.Since(start), e)
		})
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests != 4 || got.Errors != 1 || got.ThroughputRPS != 30 {
		t.Fatalf("%+v", got)
	}
	if got.LatencyMS.P50 < 2 || got.LatencyMS.Max >= 100 {
		t.Fatalf("failed requests entered latency: %+v", got.LatencyMS)
	}
	before := requests.Load()
	time.Sleep(10 * time.Millisecond)
	if requests.Load() != before {
		t.Fatal("workers continued after step")
	}
}

func TestStepBoundaryDropsCutOffRequests(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    string // The job state polls return; empty means the submit outlasts the step.
		requests uint64
	}{
		{"submit slower than the step", "", 0},
		{"approval wait cut off", "PENDING", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case tc.state == "":
					<-r.Context().Done()
				case r.URL.Path == "/api/v1/jobs":
					writeFixture(t, w, `{"job_id":"job-1"}`)
				default:
					writeFixture(t, w, `{"state":"`+tc.state+`"}`)
				}
			}))
			defer server.Close()
			w, err := loadFixture(t, workloadFixture)
			if err != nil {
				t.Fatal(err)
			}
			setTarget(t, server.URL, "key", "approver")
			e, err := newExecutor(w)
			if err != nil {
				t.Fatal(err)
			}
			defer e.client.CloseIdleConnections()
			step, err := runStep(context.Background(), ProfileStep{Concurrency: 1, Duration: "300ms"}, 0, e.execute)
			if err != nil {
				t.Fatal(err)
			}
			if step.Requests != tc.requests || step.Errors != 0 {
				t.Fatalf("%+v", step)
			}
			if got := findSaturation([]StepResult{step}, w.Saturation); got.Rule != "not reached" || got.Reached {
				t.Fatalf("%+v", got)
			}
		})
	}
}
