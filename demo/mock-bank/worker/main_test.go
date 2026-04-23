package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/sdk/runtime"
)

func newCtx(jobID, topic string) runtime.Context {
	return runtime.Context{
		Job: &agentv1.JobRequest{
			JobId: jobID,
			Topic: topic,
		},
	}
}

// waitFor spins until fn() is true or d elapses; fails the test on timeout.
// Lets us assert atomic counter transitions without racing the scheduler.
func waitFor(t *testing.T, d time.Duration, fn func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", msg)
}

func TestActiveJobsCounterReflectsInflight(t *testing.T) {
	var counter atomic.Int32
	release := make(chan struct{})
	entered := make(chan struct{}, 3)

	inner := func(_ runtime.Context, _ bankPayload) (bankResult, error) {
		entered <- struct{}{}
		<-release
		return bankResult{Status: "completed"}, nil
	}
	wrapped := newCountingHandler(&counter, inner)

	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = wrapped(newCtx("job", "job.demo-mock-bank.transfer"), bankPayload{})
		}()
	}

	for i := range 3 {
		select {
		case <-entered:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("handler %d did not enter within 500ms", i+1)
		}
	}

	waitFor(t, 500*time.Millisecond, func() bool { return counter.Load() == 3 },
		"counter never reached 3")

	close(release)
	wg.Wait()

	waitFor(t, 500*time.Millisecond, func() bool { return counter.Load() == 0 },
		"counter did not return to 0 after handlers completed")
}

func TestActiveJobsCounterDecrementsOnError(t *testing.T) {
	var counter atomic.Int32
	boom := errors.New("handler failed")
	inner := func(_ runtime.Context, _ bankPayload) (bankResult, error) {
		return bankResult{}, boom
	}
	wrapped := newCountingHandler(&counter, inner)

	_, err := wrapped(newCtx("job", "topic"), bankPayload{})
	if !errors.Is(err, boom) {
		t.Fatalf("expected handler error to propagate, got %v", err)
	}
	if got := counter.Load(); got != 0 {
		t.Errorf("counter after error = %d, want 0", got)
	}
}

func TestActiveJobsCounterDecrementsOnPanic(t *testing.T) {
	var counter atomic.Int32
	inner := func(_ runtime.Context, _ bankPayload) (bankResult, error) {
		panic("boom")
	}
	wrapped := newCountingHandler(&counter, inner)

	_, err := wrapped(newCtx("job", "topic"), bankPayload{})
	if err == nil {
		t.Fatal("expected wrapper to recover panic into error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("recovered error should mention panic reason %q, got %v", "boom", err)
	}
	if got := counter.Load(); got != 0 {
		t.Errorf("counter after panic = %d, want 0", got)
	}
}

// Structured-log tests. These pin the three per-job records the operator
// log-grep recipes in the README rely on (job_received, decision_made,
// job_completed / job_failed). The keys job_id + worker_id must appear
// on every record so SIEM joins stay cheap.

func drainLogRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("non-JSON log line: %q (%v)", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func hasKeys(rec map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := rec[k]; !ok {
			return false
		}
	}
	return true
}

func TestHandlerEmitsThreeStructuredRecordsOnSuccess(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := makeHandlerWithLogger("worker-test", "demo-mock-bank", logger, instantSleep)

	_, err := h(newCtx("job-99", "job.demo-mock-bank.transfer"),
		bankPayload{Amount: 40.0, Customer: "Alice"})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}

	records := drainLogRecords(t, buf)
	if len(records) != 3 {
		t.Fatalf("want 3 structured records, got %d: %v", len(records), records)
	}
	for i, rec := range records {
		if !hasKeys(rec, "job_id", "worker_id") {
			t.Errorf("record %d missing job_id or worker_id: %v", i, rec)
		}
	}
	mid := records[1]
	if verdict, _ := mid["verdict"].(string); verdict != "allow" {
		t.Errorf("decision record verdict = %q, want \"allow\"", verdict)
	}
	if rule, _ := mid["rule"].(string); rule != "bank-transfer-allow" {
		t.Errorf("decision record rule = %q, want \"bank-transfer-allow\"", rule)
	}
	final := records[2]
	if _, ok := final["duration_ms"]; !ok {
		t.Errorf("final record missing duration_ms: %v", final)
	}
}

func TestHandlerVerdictBuckets(t *testing.T) {
	cases := []struct {
		name       string
		amount     float64
		wantRule   string
		wantVerd   string
	}{
		{"low", 40, "bank-transfer-allow", "allow"},
		{"review", 200, "bank-transfer-review", "require_approval"},
		{"blocked", 5000, "bank-transfer-blocked", "deny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
			h := makeHandlerWithLogger("worker-test", "demo-mock-bank", logger, instantSleep)

			_, err := h(newCtx("j", "job.demo-mock-bank.transfer"), bankPayload{Amount: tc.amount})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			records := drainLogRecords(t, buf)
			if len(records) < 2 {
				t.Fatalf("want >=2 records, got %d", len(records))
			}
			mid := records[1]
			if got, _ := mid["rule"].(string); got != tc.wantRule {
				t.Errorf("rule = %q, want %q", got, tc.wantRule)
			}
			if got, _ := mid["verdict"].(string); got != tc.wantVerd {
				t.Errorf("verdict = %q, want %q", got, tc.wantVerd)
			}
		})
	}
}

// Ensures slog records do NOT include raw payload fields that could carry
// PII — redaction discipline per feedback_prod_implementations.
func TestHandlerDoesNotLogRawPayload(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := makeHandlerWithLogger("worker-test", "demo-mock-bank", logger, instantSleep)

	_, err := h(newCtx("j", "topic"), bankPayload{
		Amount:   40,
		Customer: "SENSITIVE_CUSTOMER",
		Note:     "SENSITIVE_NOTE",
		Prompt:   "SENSITIVE_PROMPT",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	body := buf.String()
	for _, secret := range []string{"SENSITIVE_CUSTOMER", "SENSITIVE_NOTE", "SENSITIVE_PROMPT"} {
		if strings.Contains(body, secret) {
			t.Errorf("log body must not contain raw payload field %q, got: %s", secret, body)
		}
	}
}

// instantSleep makes tests deterministic by replacing the handler's random
// 200–2000 ms work-simulation with a no-op. The production path keeps the
// random sleep.
func instantSleep(context.Context) {}
