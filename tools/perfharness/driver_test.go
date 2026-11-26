package perfharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestAdapters(t *testing.T) {
	const key = "distinctive-key-must-never-be-recorded"
	var polls, approvals atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != key || r.Header.Get("X-Tenant-ID") != "tenant" {
			t.Error("missing headers")
		}
		switch r.URL.Path {
		case "/api/v1/status":
			fmt.Fprint(w, `{"build":{"version":"v-test"}}`)
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
			fmt.Fprint(w, `{"job_id":"job-1"}`)
		case "/api/v1/jobs/job-1":
			if polls.Add(1) == 1 {
				fmt.Fprint(w, `{"state":"PENDING"}`)
			} else {
				fmt.Fprint(w, `{"state":"APPROVAL_REQUIRED"}`)
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
	t.Setenv("PERF_URL", server.URL)
	t.Setenv("PERF_KEY", key)
	t.Setenv("PERF_TENANT", "tenant")
	t.Setenv("CORDUM_TLS_CA", "")
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
	if err != nil || bytes.Contains(data, []byte(key)) {
		t.Fatal("key leaked or invalid results")
	}
}

func TestApprovalBoundAndUnknownVersion(t *testing.T) {
	var approvals atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "approve") {
			approvals.Add(1)
		}
		fmt.Fprint(w, `{"state":"PENDING"}`)
	}))
	defer server.Close()
	w, err := loadFixture(t, workloadFixture)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERF_URL", server.URL)
	t.Setenv("PERF_KEY", "key")
	t.Setenv("PERF_TENANT", "tenant")
	t.Setenv("CORDUM_TLS_CA", "")
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

func TestDriverRecordedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.yaml")
	if err := os.WriteFile(path, []byte(workloadFixture), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERF_URL", "http://localhost:8081")
	w, r, err := prepareResults(context.Background(), path, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Environment.Configuration.RequestMix, w.RequestMix) || !reflect.DeepEqual(r.Environment.Configuration.ProfileSteps, w.Profiles["smoke"].Steps) {
		t.Fatal("configuration changed")
	}
	r.Steps = []StepResult{{Index: 0, Concurrency: 1, DurationMS: 1000}}
	r.Saturation = findSaturation(r.Steps, w.Saturation)
	if _, err := WriteResults(*r); err != nil {
		t.Fatal(err)
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
	if got.Requests != 5 || got.Errors != 2 || got.ThroughputRPS != 30 {
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
