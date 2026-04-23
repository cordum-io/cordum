package audit

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// BenchmarkChainer_Append exercises the hot path: one Append per
// iteration against an in-process miniredis. The Lua CAS script runs
// per call, so each iteration is roughly the per-event cost callers
// pay in the audit pipeline.
//
// miniredis is synchronous pure-Go; numbers here underestimate the
// latency of a real cross-network Redis but are representative of the
// Go-side overhead (hash + marshal + Lua roundtrip).
func BenchmarkChainer_Append(b *testing.B) {
	mr, err := miniredis.Run()
	if err != nil {
		b.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = client.Close() }()
	c := NewChainer(client, "bench:chain:")

	ev := &SIEMEvent{
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  "bench-tenant",
		Action:    "bench",
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ev.Seq = 0
		ev.PrevHash = ""
		ev.EventHash = ""
		if err := c.Append(ctx, ev); err != nil {
			b.Fatalf("append: %v", err)
		}
	}
}

// TestChainer_Append10kLatency appends 10k events and reports p50/p95/p99.
// The plan targets p99<1ms on dev hardware with real Redis. Miniredis
// adds Go-side synchronization overhead, and CI runs this under -race + coverage
// where p99 can exceed the non-instrumented local ceiling. Assert a generous
// ceiling (25ms) to catch catastrophic regressions, not quibble over runner
// noise.
// The raw percentiles are logged so anyone reviewing CI output sees the
// real numbers.
func TestChainer_Append10kLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10k append latency in -short mode")
	}
	c, _, _ := newTestChainer(t)
	ctx := context.Background()

	const iterations = 10_000
	latencies := make([]time.Duration, 0, iterations)

	ev := &SIEMEvent{
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  "latency-tenant",
		Action:    "bench",
	}
	for i := 0; i < iterations; i++ {
		ev.Seq = 0
		ev.PrevHash = ""
		ev.EventHash = ""
		start := time.Now()
		if err := c.Append(ctx, ev); err != nil {
			t.Fatalf("append[%d]: %v", i, err)
		}
		latencies = append(latencies, time.Since(start))
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)*50/100]
	p95 := latencies[len(latencies)*95/100]
	p99 := latencies[len(latencies)*99/100]
	t.Logf("append latency over %d events: p50=%s p95=%s p99=%s max=%s",
		iterations, p50, p95, p99, latencies[len(latencies)-1])

	// Ceiling chosen to fail on egregious regressions (25ms p99 in a
	// pure-Go miniredis loop, even under CI -race instrumentation, would
	// indicate a pathological Lua retry storm or a new allocation on the hot
	// path). The plan's <1ms target applies to real Redis; asserting it
	// against miniredis would be flaky on slow CI.
	const p99Ceiling = 25 * time.Millisecond
	if p99 > p99Ceiling {
		t.Errorf("p99 append latency %s exceeds %s ceiling", p99, p99Ceiling)
	}
}
