package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cordum/cordum/core/audit"
)

type noopSender struct{}

func (noopSender) Send(audit.SIEMEvent) {}
func (noopSender) Close() error         { return nil }

func BenchmarkInvocationAuditor(b *testing.B) {
	sender := noopSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())
	args := json.RawMessage(`{"user":"alice","password":"s3cr3t","note":"invoke job.echo with topic","meta":{"req_id":"abc-123","token":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.abc"}}`)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, h := a.StartInbound(ctx, "agent-1", "tenant-a", "jobs.submit", args)
		a.FinishInbound(h, &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil)
	}
}

// TestHighVolume_NoDroppedEvents fires 10k concurrent invocations
// through a shared auditor and confirms every one produces exactly
// one SIEMEvent. Guards against data races and map sharing.
func TestHighVolume_NoDroppedEvents(t *testing.T) {
	t.Parallel()

	var counter atomic.Int64
	sender := &countingSender{counter: &counter}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	const fireCount = 10_000
	const goroutines = 32
	each := fireCount / goroutines
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < each; i++ {
				args := json.RawMessage(`{"i":` + itoa(int64(g*each+i)) + `,"password":"x"}`)
				_, h := a.StartInbound(ctx, "agent", "tenant-a", "tool", args)
				a.FinishInbound(h, &ToolCallResult{}, nil)
			}
		}()
	}
	wg.Wait()
	if got := counter.Load(); got != int64(goroutines*each) {
		t.Errorf("dropped events: counter=%d, want %d", got, goroutines*each)
	}
}

type countingSender struct {
	counter *atomic.Int64
	// Keep one reference so the benchmark doesn't allocate a new
	// sender per iteration — pins the hot-path allocation budget.
}

func (c *countingSender) Send(audit.SIEMEvent) { c.counter.Add(1) }
func (c *countingSender) Close() error         { return nil }
