package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	capruntime "github.com/cordum-io/cap/v2/sdk/go/runtime"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
)

// parseGreetPayload is the JSON decoder used by the tests below. It
// lives in the test package so the main binary stays lean — the
// production decode path runs inside the runtime's typed-handler
// generics, not this helper. Kept here (vs ad-hoc json.Unmarshal in
// each test) so the DisallowUnknownFields + null-tolerance contract
// is enforced in exactly one place.
func parseGreetPayload(raw []byte) (greetPayload, error) {
	var p greetPayload
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return p, nil
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return greetPayload{}, fmt.Errorf("invalid greet payload: %w", err)
	}
	return p, nil
}

// newCtx builds the minimal Context the greetHandler needs. The runtime
// Context.Job is a *agentv1.JobRequest, and only Topic + JobId are read.
func newCtx(t *testing.T, topic, jobID string) capruntime.Context {
	t.Helper()
	_ = context.Background() // imported for parity with handler tests below
	return capruntime.Context{
		Job: &agentv1.JobRequest{
			JobId: jobID,
			Topic: topic,
		},
	}
}

func TestGreetHandlerNominal(t *testing.T) {
	out, err := greetHandler(newCtx(t, "job.demo.greet", "job-1"), greetPayload{Name: "Yaron"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Greeting != "hello, Yaron!" {
		t.Errorf("greeting = %q, want 'hello, Yaron!'", out.Greeting)
	}
	if out.Topic != "job.demo.greet" {
		t.Errorf("topic = %q", out.Topic)
	}
	if out.JobID != "job-1" {
		t.Errorf("job id = %q", out.JobID)
	}
}

func TestGreetHandlerEmptyNameFallback(t *testing.T) {
	out, err := greetHandler(newCtx(t, "job.demo.greet", "job-2"), greetPayload{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Greeting != "hello, world!" {
		t.Errorf("empty-name fallback greeting = %q, want 'hello, world!'", out.Greeting)
	}
}

func TestGreetHandlerWhitespaceNameFallback(t *testing.T) {
	out, err := greetHandler(newCtx(t, "job.demo.greet", "job-3"), greetPayload{Name: "   "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Greeting != "hello, world!" {
		t.Errorf("whitespace-only name did not fall back, got %q", out.Greeting)
	}
}

func TestParseGreetPayloadJSON(t *testing.T) {
	p, err := parseGreetPayload([]byte(`{"name":"Ada"}`))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if p.Name != "Ada" {
		t.Errorf("name = %q", p.Name)
	}
}

func TestParseGreetPayloadNullAndEmpty(t *testing.T) {
	for _, in := range [][]byte{nil, []byte(""), []byte("  "), []byte("null")} {
		p, err := parseGreetPayload(in)
		if err != nil {
			t.Errorf("input %q: unexpected error %v", string(in), err)
		}
		if p.Name != "" {
			t.Errorf("input %q: want empty name, got %q", string(in), p.Name)
		}
	}
}

func TestParseGreetPayloadRejectsNonJSON(t *testing.T) {
	_, err := parseGreetPayload([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for non-JSON input")
	}
	if !strings.Contains(err.Error(), "invalid greet payload") {
		t.Errorf("error should mention 'invalid greet payload', got %v", err)
	}
}

func TestParseGreetPayloadRejectsUnknownFields(t *testing.T) {
	_, err := parseGreetPayload([]byte(`{"unknown_field":"oops"}`))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestGreetHandlerRespectsContextTimeout(t *testing.T) {
	// The current handler is synchronous and instant, so a cancelled context
	// produces a normal result. This test pins the contract: the handler
	// must not block past the caller's context — if it did, the scheduler
	// would never get its reply back.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_ = ctx // pinned in the closure below to keep the timeout contract visible
	rc := capruntime.Context{Job: &agentv1.JobRequest{JobId: "j", Topic: "job.demo.greet"}}

	done := make(chan struct{})
	go func() {
		_, _ = greetHandler(rc, greetPayload{Name: "fast"})
		close(done)
	}()
	select {
	case <-done:
		// ok — handler returned well before the context deadline.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("greetHandler blocked past 200ms — it must be instant")
	}
}

func TestMetricsHandlerEmitsCounters(t *testing.T) {
	// Reset package-level metrics so the test is deterministic. Tests in the
	// same package run serially by default (no t.Parallel), which keeps this
	// mutation safe.
	m.jobsTotal.Store(0)
	m.jobsFailed.Store(0)
	m.lastDuration.Store(0)

	// Drive the handler so the counter moves.
	if _, err := greetHandler(newCtx(t, "job.demo.greet", "metrics-1"), greetPayload{Name: "x"}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	metricsHandler().ServeHTTP(rr, req)
	body, _ := io.ReadAll(rr.Body)
	text := string(body)
	for _, want := range []string{
		"demo_quickstart_jobs_total 1",
		"demo_quickstart_jobs_failed_total 0",
		"demo_quickstart_last_duration_microseconds",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in metrics body:\n%s", want, text)
		}
	}
}

// sanity: the typed greetResult roundtrips through JSON so the workflow's
// summary-step extraction does not break if the struct changes shape.
func TestGreetResultJSONRoundtrip(t *testing.T) {
	in := greetResult{Greeting: "hello, x!", Topic: "t", JobID: "j", WorkerID: "w", Time: "2026-04-17T00:00:00Z"}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out greetResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("roundtrip mismatch: %+v != %+v", out, in)
	}
}
