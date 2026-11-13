package perfharness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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

func syntheticResults(t *testing.T) Results {
	t.Helper()
	return Results{
		SchemaVersion: 1,
		Run:           RunInfo{ID: "fixture", StartedAt: "2026-01-01T00:00:00Z", FinishedAt: "2026-01-01T00:00:01Z", Profile: "smoke"},
		Workload:      WorkloadInfo{Name: "fixture", File: "fixture.yaml", SHA256: strings.Repeat("a", 64)},
		Environment: Environment{
			Hardware: Hardware{OS: "linux", Arch: "amd64", CPUModel: "fixture", LogicalCPUs: 4, Fingerprint: "235e525443d822e61f5c16adb8857aa242196837925d58b9d32cf5e0886761e8"},
			Versions: Versions{HarnessRevision: "unknown", GoVersion: "fixture", Target: TargetVersion{Version: "unknown", Source: "unknown"}},
			Configuration: Configuration{
				TargetBaseURL: "http://localhost:8081", EnvVarsRead: []string{"PERF_KEY"},
				RequestMix:   []Sequence{{Name: "submit", Weight: 1, Requests: []Request{{Kind: "jobs.submit", Payload: map[string]any{"prompt": "fixture"}}}}},
				ProfileSteps: []ProfileStep{{Concurrency: 1, Duration: "1s"}},
				Saturation:   Thresholds{P99MS: 1000, ErrorRate: 0.01, MinThroughputGainPct: 5},
				QueueDepth:   QueueSource{Kind: "nats_jsz", URLEnv: "PERF_QUEUE_URL", Stream: "CORDUM_JOBS", IntervalMS: 100},
			},
		},
		Steps:      []StepResult{{Index: 0, Concurrency: 1, DurationMS: 1000, Requests: 10, Errors: 1, ThroughputRPS: 10, LatencyMS: Latency{P50: 1, P90: 2, P95: 3, P99: 4, Max: 5}}},
		QueueDepth: QueueDepth{Source: "nats_jsz", IntervalMS: 100, Samples: []QueueSample{{TMS: 0, Value: nil}}},
		Saturation: SaturationResult{Rule: "fixture", Reason: "not reached"},
	}
}

func TestResultsRoundTrip(t *testing.T) {
	want := syntheticResults(t)
	for _, reached := range []bool{false, true} {
		if reached {
			index := 0
			want.Saturation.Reached, want.Saturation.StepIndex = true, &index
			// The second pass also writes the nil slices a driver may leave unset.
			want.Environment.Configuration.EnvVarsRead, want.QueueDepth.Samples = nil, nil
		}
		data, err := WriteResults(want)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseResults(data)
		if err != nil || !reflect.DeepEqual(&want, got) {
			t.Fatalf("round trip: %+v, %v", got, err)
		}
		if got, err := ParseResults(append(data, []byte(" {}")...)); got != nil || err == nil {
			t.Fatal("trailing JSON accepted")
		}
	}
}

func TestResultsRejects(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Results)
		rule   string
	}{
		{"percentiles", func(r *Results) { r.Steps[0].LatencyMS.P50 = 6 }, "percentile"},
		{"errors", func(r *Results) { r.Steps[0].Errors = 11 }, "errors"},
		{"indexes", func(r *Results) { r.Steps[0].Index = 1 }, "index"},
		{"saturated missing", func(r *Results) { r.Saturation.Reached = true }, "step_index"},
		{"saturated out of range", func(r *Results) { i := 1; r.Saturation.Reached = true; r.Saturation.StepIndex = &i }, "step_index"},
		{"unsaturated index", func(r *Results) { i := 0; r.Saturation.StepIndex = &i }, "step_index"},
		{"fingerprint", func(r *Results) { r.Environment.Hardware.CPUModel = "changed" }, "fingerprint"},
		{"secret", func(r *Results) { r.Environment.Configuration.RequestMix[0].Requests[0].Payload["SECRET"] = "sentinel" }, "secret"},
		{"url credentials", func(r *Results) { r.Environment.Configuration.TargetBaseURL = "https://user:sentinel@example.test" }, "target_base_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := syntheticResults(t)
			tc.change(&r)
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseResults(data)
			if got != nil || err == nil || !strings.Contains(err.Error(), tc.rule) {
				t.Fatalf("want %s: %v", tc.rule, err)
			}
			if released, err := WriteResults(r); err == nil || len(released) != 0 {
				t.Fatal("invalid result released bytes")
			}
		})
	}
	for _, key := range []string{"hardware", "versions", "configuration", "unknown"} {
		t.Run(key, func(t *testing.T) { rejectResultsShape(t, key) })
	}
}

func rejectResultsShape(t *testing.T, key string) {
	t.Helper()
	data, err := json.Marshal(syntheticResults(t))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(document["environment"], &env); err != nil {
		t.Fatal(err)
	}
	if key == "unknown" {
		env[key] = json.RawMessage(`1`)
	} else {
		delete(env, key)
	}
	document["environment"], err = json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ParseResults(data); got != nil || err == nil {
		t.Fatal("invalid schema accepted")
	}
}

func TestWorkloadFilesLoad(t *testing.T) {
	files, err := filepath.Glob("workloads/*.yaml")
	if err != nil || len(files) < 4 {
		t.Fatalf("need four workload files: %v, %v", files, err)
	}
	names, kinds := make(map[string]bool), make(map[string]bool)
	for _, file := range files {
		w, err := LoadWorkload(file)
		if err != nil {
			t.Fatal(err)
		}
		if names[w.Name] || len(w.RequestMix) == 0 || len(w.Prerequisites) == 0 {
			t.Fatalf("invalid workload %s", file)
		}
		names[w.Name] = true
		for _, sequence := range w.RequestMix {
			for _, request := range sequence.Requests {
				kinds[request.Kind] = true
			}
		}
		assertRecordable(t, w)
	}
	for _, kind := range []string{"jobs.submit", "jobs.approve", "edge.hook.evaluate", "mcp.tools.call"} {
		if !kinds[kind] {
			t.Fatalf("missing request kind %s", kind)
		}
	}
	if enum, err := json.Marshal(KnownRequestKinds); err != nil || !strings.Contains(string(resultsSchema), `"kind":{"enum":`+string(enum)+"}") {
		t.Fatalf("results schema kind enum must equal KnownRequestKinds %s", enum)
	}
}

// assertRecordable is the cross-contract check: every profile of a loaded workload
// must record into a results file that validates and parses back unchanged.
func assertRecordable(t *testing.T, w *Workload) {
	t.Helper()
	for name, profile := range w.Profiles {
		want := syntheticResults(t)
		want.Run.Profile, want.Workload.Name, want.Steps[0].Concurrency = name, w.Name, int(profile.Steps[0].Concurrency)
		want.QueueDepth.Source, want.QueueDepth.IntervalMS = w.QueueDepth.Kind, int(w.QueueDepth.IntervalMS)
		c := &want.Environment.Configuration
		c.RequestMix, c.ProfileSteps, c.Saturation, c.QueueDepth = w.RequestMix, profile.Steps, w.Saturation, w.QueueDepth
		data, err := WriteResults(want)
		if err != nil {
			t.Fatalf("%s/%s: %v", w.Name, name, err)
		}
		if got, err := ParseResults(data); err != nil || !reflect.DeepEqual(&want, got) {
			t.Fatalf("%s/%s round trip: %v", w.Name, name, err)
		}
	}
}

func TestEnvironmentProbeParsers(t *testing.T) {
	for _, tc := range []struct {
		source, cpu, memory, wantCPU string
		wantMemory                   uint64
	}{
		{"linux", "model name\t: Fixture CPU\n", "MemTotal: 1024 kB\n", "Fixture CPU", 1048576},
		{"darwin", "Fixture CPU\n", "1048576\n", "Fixture CPU", 1048576},
		{"windows", "Fixture CPU\r\n", "1048576\r\n", "Fixture CPU", 1048576},
		{"linux", "garbage", "garbage", "unknown", 0},
		{"darwin", "\x00", "garbage", "unknown", 0},
		{"windows", "\x00", "0", "unknown", 0},
	} {
		t.Run(tc.source+tc.wantCPU, func(t *testing.T) {
			if got := parseCPU(tc.source, tc.cpu); got != tc.wantCPU {
				t.Fatalf("CPU %q", got)
			}
			got := parseMemory(tc.source, tc.memory)
			if tc.wantMemory == 0 {
				if got != nil {
					t.Fatal("failed probe must be null")
				}
			} else if got == nil || *got != tc.wantMemory {
				t.Fatalf("memory %v", got)
			}
		})
	}
}

func TestCaptureEnvironmentLive(t *testing.T) {
	e := CaptureEnvironment(context.Background())
	if e.Hardware.LogicalCPUs < 1 || e.Hardware.OS == "" || e.Hardware.Arch == "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(e.Hardware.Fingerprint) {
		t.Fatalf("invalid hardware: %+v", e.Hardware)
	}
	if e.Hardware.MemoryBytes != nil && *e.Hardware.MemoryBytes == 0 {
		t.Fatal("zero memory is not unknown")
	}
	if e.Versions.GoVersion == "" || e.Versions.Target.Source != "unknown" {
		t.Fatal("invalid versions")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e = CaptureEnvironment(ctx)
	if e.Hardware.CPUModel != "unknown" || e.Hardware.MemoryBytes != nil {
		t.Fatal("cancelled probes must not fabricate hardware")
	}
}

func TestWorkloadLoadValid(t *testing.T) {
	// The deepest payload a results document holds; TestWorkloadRejects/deep_payload pins one level deeper.
	deep := strings.Replace(workloadFixture, "topic: job.perf", "nested: "+strings.Repeat("[", 56)+"x"+strings.Repeat("]", 56), 1)
	for _, content := range []string{workloadFixture, strings.ReplaceAll(workloadFixture, "\n", "\r\n"), deep} {
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
		if w.RequestMix[0].Requests[0].Payload["prompt"] != "{{uuid}}-{{seq}}" || w.RequestMix[0].Requests[1].PathParams["job_id"] != "{{capture.job_id}}" {
			t.Fatal("payload/capture templates changed")
		}
		assertRecordable(t, w)
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
		{"missing sequence name", "- name: submit-approve\n    weight: 1", "- weight: 1", "request_mix[0].name: must not be empty"},
		{"null payload", "payload: {}", "payload: null", "request_mix[0].requests[1].payload: must be an object"},
		{"missing payload", "\n        payload: {}", "", "request_mix[0].requests[1].payload: must be an object"},
		{"empty profile name", "standard:", "'':", "profiles: names must not be empty"},
		{"forward capture", "{{uuid}}-{{seq}}", "{{capture.job_id}}", "earlier"},
		{"missing capture", "{{capture.job_id}}", "{{capture.missing}}", "earlier"},
		{"unknown token", "{{uuid}}-{{seq}}", "{{password}}", "token"},
		{"unfinished token", "{{uuid}}-{{seq}}", "{{uuid}", "token"},
		{"smoke missing", "smoke:", "tiny:", "smoke"},
		{"smoke not smallest", "concurrency: 1, duration: 1s", "concurrency: 9, duration: 9s", "smallest"},
		{"zero concurrency", "concurrency: 1", "concurrency: 0", "concurrency"},
		{"fractional concurrency", "concurrency: 1", "concurrency: 1.5", "concurrency"},
		{"zero duration", "duration: 1s", "duration: 0s", "duration"},
		{"invalid duration", "duration: 1s", "duration: later", "duration"},
		{"p99 threshold", "p99_ms: 1000", "p99_ms: 0", "p99_ms"},
		{"nonfinite threshold", "p99_ms: 1000", "p99_ms: .nan", "p99_ms"},
		{"error rate zero", "error_rate: 0.01", "error_rate: 0", "error_rate"},
		{"error rate high", "error_rate: 0.01", "error_rate: 2", "error_rate"},
		{"gain", "min_throughput_gain_pct: 5", "min_throughput_gain_pct: -1", "min_throughput_gain_pct"},
		{"tolerance throughput", "throughput_pct: 10", "throughput_pct: 0", "throughput_pct"},
		{"tolerance p99", "p99_pct: 10", "p99_pct: 0", "p99_pct"},
		{"queue kind", "kind: nats_jsz", "kind: redis", "queue_depth"},
		{"queue stream", "stream: CORDUM_JOBS", "stream: ''", "stream"},
		{"queue metric", "kind: nats_jsz", "kind: prometheus", "metric"},
		{"queue interval", "interval_ms: 100", "interval_ms: 0", "interval_ms"},
		{"fractional interval", "interval_ms: 100", "interval_ms: 100.5", "interval_ms"},
		{"secret payload", "prompt:", "Password:", "secret"},
		{"nested secret", "topic: job.perf", "nested: [{ToKeN: forbidden}]", "secret"},
		{"deep payload", "topic: job.perf", "nested: " + strings.Repeat("[", 57) + "0" + strings.Repeat("]", 57), "nesting"},
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
		{"empty name", func(w *Workload) { w.Name = "" }, "name: must not be empty"},
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
