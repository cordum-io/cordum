package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestFilterCache_HitAndInvalidation(t *testing.T) {
	t.Parallel()

	r := NewToolRegistry()
	handler := func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) { return &ToolCallResult{}, nil }
	if err := r.Register(Tool{Name: "fs.read", RiskTier: "low"}, handler); err != nil {
		t.Fatalf("register: %v", err)
	}

	id := &AgentIdentity{ID: "a", RiskTier: "high", AllowedTools: []string{"*"}}
	ctx := ContextWithIdentity(context.Background(), id)

	// Prime the cache.
	first := r.ListTools(ctx)
	if len(first) != 1 {
		t.Fatalf("baseline: want 1 tool, got %d", len(first))
	}

	// Cache hit should return the same slice header (same backing array).
	second := r.ListTools(ctx)
	if len(second) != 1 {
		t.Fatalf("cached: want 1 tool, got %d", len(second))
	}
	if &first[0] != &second[0] {
		t.Errorf("cache miss: second call should return the same backing slice as first")
	}

	// Register a new tool — cache still holds the old filtered output until
	// SetConfig bumps the version or TTL expires. Register does NOT
	// currently invalidate (matches the design: new tools require a
	// SetConfig/boot to be picked up via the runtime policy surface).
	if err := r.Register(Tool{Name: "fs.write", RiskTier: "low"}, handler); err != nil {
		t.Fatalf("register fs.write: %v", err)
	}

	// SetConfig bumps the cache version — next call should recompute.
	beforeVersion := r.cache.currentVersion()
	r.SetConfig(map[string]any{"unrelated": true})
	if r.cache.currentVersion() == beforeVersion {
		t.Fatalf("SetConfig should bump cache version")
	}
	third := r.ListTools(ctx)
	if len(third) != 2 {
		t.Fatalf("after SetConfig: want 2 tools (recomputed), got %d — %v", len(third), toolNames(third))
	}
}

func TestFilterCache_IdentityChangeBypassesCache(t *testing.T) {
	t.Parallel()

	r := NewToolRegistry()
	handler := func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) { return &ToolCallResult{}, nil }
	for _, name := range []string{"fs.read", "jobs.submit", "nuke.all"} {
		tier := "low"
		if name == "nuke.all" {
			tier = "critical"
		}
		if err := r.Register(Tool{Name: name, RiskTier: tier}, handler); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	lowID := &AgentIdentity{ID: "l", RiskTier: "low", AllowedTools: []string{"*"}}
	critID := &AgentIdentity{ID: "c", RiskTier: "critical", AllowedTools: []string{"*"}}

	// First identity sees only low-tier tools.
	got := r.ListTools(ContextWithIdentity(context.Background(), lowID))
	if len(got) != 2 {
		t.Fatalf("low-tier: want 2, got %d", len(got))
	}

	// Different identity must NOT share the cache entry even though the
	// registry and config haven't changed.
	got = r.ListTools(ContextWithIdentity(context.Background(), critID))
	if len(got) != 3 {
		t.Fatalf("critical-tier: want 3, got %d", len(got))
	}
}

func TestFilterCache_NilIdentityCachedAsEmpty(t *testing.T) {
	t.Parallel()

	r := NewToolRegistry()
	if err := r.Register(Tool{Name: "fs.read", RiskTier: "low"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	got := r.ListTools(context.Background())
	if len(got) != 0 {
		t.Fatalf("nil identity: want 0, got %d", len(got))
	}
	// Second call still zero and served from cache.
	got = r.ListTools(context.Background())
	if len(got) != 0 {
		t.Fatalf("nil identity cached: want 0, got %d", len(got))
	}
}

// TestFilterCache_KeyForConsistentUnderConcurrentReads asserts the
// immutable-after-publish contract on *AgentIdentity: once a producer
// hands an identity off to the filter path, the slice fields it sets
// (AllowedTools, DataClassifications) must never be reassigned.
//
// keyFor reads both slices via `append([]string{}, id.X...)` without
// holding any lock on the identity. The two production producers
// (gateway/mcp_identity.go::mcpIdentityFromStore and
// mcp/tools/register.go) freshly allocate the identity and copy the
// slice contents at construction, then publish a pointer that no
// later code path mutates. That invariant is documented in the
// AgentIdentity doc comment in filter.go.
//
// This test pins the invariant in place: 1000 concurrent callers
// must observe the same key for an unmutated identity. A regression
// that introduced post-publish slice mutation would race with these
// readers and produce inconsistent keys (or trip -race).
func TestFilterCache_KeyForConsistentUnderConcurrentReads(t *testing.T) {
	t.Parallel()

	id := &AgentIdentity{
		ID:                  "agent-immutable",
		RiskTier:            "high",
		AllowedTools:        []string{"fs.read", "fs.write", "jobs.submit", "policy.*"},
		DataClassifications: []string{"pii", "phi", "secrets"},
	}
	c := newFilterCache()

	const goroutines = 1000
	keys := make([]string, goroutines)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			keys[i] = c.keyFor(id)
		}(i)
	}
	close(start)
	wg.Wait()

	want := keys[0]
	if want == "" {
		t.Fatalf("keys[0] is empty")
	}
	for i := 1; i < goroutines; i++ {
		if keys[i] != want {
			t.Fatalf("keyFor inconsistent across concurrent reads: keys[0]=%q keys[%d]=%q", want, i, keys[i])
		}
	}
}
