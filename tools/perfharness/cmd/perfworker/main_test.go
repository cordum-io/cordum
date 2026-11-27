package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capruntime "github.com/cordum-io/cap/v2/sdk/go/runtime"
	captesting "github.com/cordum-io/cap/v2/sdk/go/testing"
	"github.com/cordum/cordum/sdk/runtime"
)

func newWorker(ctx context.Context, serviceTime time.Duration) *perfWorker {
	return &perfWorker{ctx: ctx, serviceTime: serviceTime, counts: newCounter()}
}

func jobCtx(topic string) runtime.Context {
	return runtime.Context{Job: &agentv1.JobRequest{JobId: "job-1", Topic: topic}}
}

// DoD 1: job.perf succeeds after the service time; job.perf.fail fails on
// every attempt; another topic, or a shutdown during the wait, is an error.
func TestHandlerTopics(t *testing.T) {
	const serviceTime = 30 * time.Millisecond
	w := newWorker(context.Background(), serviceTime)
	input := map[string]any{requestIDField: "r1"}

	start := time.Now()
	out, err := w.handle(jobCtx(topicPerf), input)
	if err != nil {
		t.Fatalf("job.perf: %v", err)
	}
	if elapsed := time.Since(start); elapsed < serviceTime {
		t.Fatalf("job.perf returned after %s, before the %s service time", elapsed, serviceTime)
	}
	if want := (perfResult{RequestID: "r1", Topic: topicPerf}); out != want {
		t.Fatalf("job.perf result = %+v, want %+v", out, want)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := w.handle(jobCtx(topicPerfFail), input); err == nil {
			t.Fatalf("job.perf.fail attempt %d succeeded", attempt)
		}
	}
	if _, err := w.handle(jobCtx("job.other"), input); err == nil {
		t.Fatal("an unsupported topic succeeded")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stopping := newWorker(ctx, time.Hour)
	if _, err := stopping.handle(jobCtx(topicPerf), input); !errors.Is(err, context.Canceled) {
		t.Fatalf("job.perf during shutdown: err = %v, want context.Canceled", err)
	}
}

// startAgent runs the worker's own registration on the real CAP runtime over
// an in-memory bus and blob store.
func startAgent(t *testing.T, w *perfWorker, retries int) (*runtime.InMemoryBus, *runtime.InMemoryBlobStore) {
	t.Helper()
	bus := runtime.NewInMemoryBus()
	store := runtime.NewInMemoryBlobStore()
	agent := runtime.NewLegacyUnsignedAgent("", "", "perfworker-test", bus, store)
	agent.HandshakeMode = capruntime.HandshakeModeOff
	register(agent, w, retries)
	if err := agent.Start(); err != nil {
		t.Fatalf("agent start: %v", err)
	}
	return bus, store
}

func submit(t *testing.T, bus *runtime.InMemoryBus, store *runtime.InMemoryBlobStore, jobID, topic string, input any) agentv1.JobStatus {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	if err := store.Set(context.Background(), "ctx:"+jobID, payload); err != nil {
		t.Fatalf("store input: %v", err)
	}
	req := &agentv1.JobRequest{JobId: jobID, Topic: topic, ContextPtr: "redis://ctx:" + jobID}
	out, err := captesting.SubmitToRuntime(bus, req)
	if err != nil || out.GetJobResult().GetJobId() != jobID {
		t.Fatalf("submit %s: no result for the job (err=%v, packet=%v)", jobID, err, out)
	}
	return out.GetJobResult().GetStatus()
}

// DoD 2: with handler retries R, one job.perf.fail job counts R+1 runs.
func TestRuntimeRetriesCountEveryRun(t *testing.T) {
	for _, retries := range []int{0, 2} {
		w := newWorker(context.Background(), 0)
		bus, store := startAgent(t, w, retries)
		status := submit(t, bus, store, "job-fail", topicPerfFail, map[string]any{requestIDField: "req-1"})
		if status != agentv1.JobStatus_JOB_STATUS_FAILED {
			t.Fatalf("R=%d: status = %v, want FAILED", retries, status)
		}
		if got := w.counts.snapshot().Counts["req-1"]; got != int64(retries+1) {
			t.Fatalf("R=%d: counted %d runs, want %d", retries, got, retries+1)
		}
	}
}

// DoD 2: jobs that share a perf_request_id add up, whether the id is at the
// top level (workflow step input) or under "context" (gateway submit).
func TestSharedRequestIDAddsUp(t *testing.T) {
	const retries = 2
	w := newWorker(context.Background(), 0)
	bus, store := startAgent(t, w, retries)

	if s := submit(t, bus, store, "job-a", topicPerfFail, map[string]any{requestIDField: "shared"}); s != agentv1.JobStatus_JOB_STATUS_FAILED {
		t.Fatalf("job-a status = %v, want FAILED", s)
	}
	gatewayInput := map[string]any{"prompt": "", "context": map[string]any{requestIDField: "shared"}}
	if s := submit(t, bus, store, "job-b", topicPerf, gatewayInput); s != agentv1.JobStatus_JOB_STATUS_SUCCEEDED {
		t.Fatalf("job-b status = %v, want SUCCEEDED", s)
	}
	submit(t, bus, store, "job-c", topicPerf, map[string]any{"context": "not an object"})
	submit(t, bus, store, "job-d", topicPerf, nil)

	want := countsDoc{Counts: map[string]int64{"shared": retries + 1 + 1}, Unlabelled: 2}
	if got := w.counts.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if err := ln.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return port
}

// DoD 3: a non-loopback address is refused and nothing listens on its port.
// 192.0.2.1 comes first: if the check ever accepted it, the bind would fail
// with a different error and t.Fatalf would stop the test before any
// wildcard address could bind (and prompt the Windows firewall).
func TestListenCountsRefusesNonLoopback(t *testing.T) {
	port := freePort(t)
	for _, host := range []string{"192.0.2.1", "0.0.0.0", "", "::", "example.com"} {
		addr := net.JoinHostPort(host, port)
		ln, err := listenCounts(addr)
		if !errors.Is(err, errNotLoopback) || ln != nil {
			if ln != nil {
				_ = ln.Close()
			}
			t.Fatalf("listenCounts(%q) = (%v, %v), want errNotLoopback and no listener", addr, ln, err)
		}
	}
	// A wildcard listener left open by any refusal would accept this.
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 2*time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("after the refusals, something accepts connections on port %s", port)
	}
}

// DoD 3: the loopback endpoint serves the counts as JSON.
func TestCountsEndpointServesJSON(t *testing.T) {
	ln, err := listenCounts("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listenCounts: %v", err)
	}
	c := newCounter()
	c.inc("a")
	c.inc("a")
	c.inc("")
	srv := &http.Server{Handler: countsHandler(c), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	resp, err := http.Get("http://" + ln.Addr().String() + "/counts")
	if err != nil {
		t.Fatalf("GET /counts: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /counts: status %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var got countsDoc
	dec := json.NewDecoder(resp.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode counts: %v", err)
	}
	if want := (countsDoc{Counts: map[string]int64{"a": 2}, Unlabelled: 1}); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
}

func TestParseFlags(t *testing.T) {
	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if want := (config{serviceTime: 10 * time.Millisecond, countsAddr: "127.0.0.1:9092"}); cfg != want {
		t.Fatalf("defaults = %+v, want %+v", cfg, want)
	}
	cfg, err = parseFlags([]string{"--service-time=5ms", "--handler-retries=3"})
	if err != nil || cfg.serviceTime != 5*time.Millisecond || cfg.handlerRetries != 3 {
		t.Fatalf("parse = (%+v, %v)", cfg, err)
	}
	if _, err := parseFlags([]string{"--handler-retries=-1"}); err == nil {
		t.Fatal("negative --handler-retries was accepted")
	}
}
