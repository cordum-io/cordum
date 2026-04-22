package mcp

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
)

// benchmarkRegistry registers count tools with varying risk tiers so
// the filter exercises every gate. Handlers are no-ops.
func benchmarkRegistry(count int) *ToolRegistry {
	r := NewToolRegistry()
	handler := func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) { return &ToolCallResult{}, nil }
	tiers := []string{"low", "medium", "high", "critical"}
	for i := 0; i < count; i++ {
		tool := Tool{
			Name:     "tool.bench." + strconv.Itoa(i),
			RiskTier: tiers[i%len(tiers)],
		}
		if i%7 == 0 {
			tool.DataClassifications = []string{"pii"}
		}
		if err := r.Register(tool, handler); err != nil {
			panic(err)
		}
	}
	return r
}

// BenchmarkListTools_200_Cached is the regression guard referenced in
// the plan: ListTools over a 200-tool registry must complete in under
// ~50µs once the per-identity cache is warm. A regression would
// indicate the cache no longer serves hits (stale key, lock
// contention, accidental recompute).
func BenchmarkListTools_200_Cached(b *testing.B) {
	r := benchmarkRegistry(200)
	id := &AgentIdentity{
		ID:                  "bench-admin",
		RiskTier:            "critical",
		AllowedTools:        []string{"*"},
		DataClassifications: []string{"pii"},
	}
	ctx := ContextWithIdentity(context.Background(), id)
	// Prime cache.
	_ = r.ListTools(ctx)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.ListTools(ctx)
	}
}

// BenchmarkListTools_200_Uncached exercises the cold-path filter: it
// bumps the cache version every iteration so each call recomputes.
// Target: under ~500µs for 200 tools.
func BenchmarkListTools_200_Uncached(b *testing.B) {
	r := benchmarkRegistry(200)
	id := &AgentIdentity{
		ID:                  "bench-admin",
		RiskTier:            "critical",
		AllowedTools:        []string{"*"},
		DataClassifications: []string{"pii"},
	}
	ctx := ContextWithIdentity(context.Background(), id)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.cache.bumpVersion()
		_ = r.ListTools(ctx)
	}
}
