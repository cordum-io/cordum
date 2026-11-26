package perfharness

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

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
