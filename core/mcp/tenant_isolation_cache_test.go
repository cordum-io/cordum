package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// TestTenantIsolationMCPFilterCache: the filter-cache key holds no tenant.
// One registry serves every tenant, so tenant-b presenting tenant-a's agent id
// must still get the tools its own identity admits.
func TestTenantIsolationMCPFilterCache(t *testing.T) {
	r := NewToolRegistry()
	handler := func(context.Context, json.RawMessage) (*ToolCallResult, error) { return &ToolCallResult{}, nil }
	for _, tool := range []Tool{
		{Name: "iso.low", RiskTier: "low"},
		{Name: "iso.high", RiskTier: "high"},
		{Name: "iso.pii", RiskTier: "low", DataClassifications: []string{"pii"}},
	} {
		if err := r.Register(tool, handler); err != nil {
			t.Fatalf("register %s: %v", tool.Name, err)
		}
	}
	list := func(tenant string, id *AgentIdentity) []Tool {
		return r.ListTools(ContextWithIdentity(WithTenant(context.Background(), tenant), id))
	}
	idA := &AgentIdentity{ID: "agent-iso", RiskTier: "high", AllowedTools: []string{"*"}, DataClassifications: []string{"pii"}}

	first := list("tenant-a", idA)
	if got := toolNames(first); !slices.Equal(got, []string{"iso.high", "iso.low", "iso.pii"}) {
		t.Fatalf("tenant-a tools = %v, want all three", got)
	}
	if hit := list("tenant-a", idA); &hit[0] != &first[0] {
		t.Fatal("tenant-a repeat was recomputed, want the cached slice")
	}

	narrow := list("tenant-b", &AgentIdentity{ID: "agent-iso", RiskTier: "low", AllowedTools: []string{"*"}})
	if got := toolNames(narrow); !slices.Equal(got, []string{"iso.low"}) || &narrow[0] == &first[0] {
		t.Fatalf("tenant-b with tenant-a's agent id got %v, want only its own iso.low", got)
	}

	// An exact repeat shares tenant-a's key. The key covers every identity
	// field FilterForIdentity reads, so the shared entry must equal what
	// tenant-b's own identity computes from the install-wide registry.
	idB := &AgentIdentity{ID: "agent-iso", RiskTier: "high", AllowedTools: []string{"*"}, DataClassifications: []string{"pii"}}
	repeat := list("tenant-b", idB)
	if own := FilterForIdentity(r.ListToolsUnfiltered(), idB); !reflect.DeepEqual(toolNames(repeat), toolNames(own)) {
		t.Fatalf("tenant-b exact repeat = %v, want its own computation %v", toolNames(repeat), toolNames(own))
	}
	if again := list("tenant-a", idA); &again[0] != &first[0] || len(again) != 3 {
		t.Fatalf("tenant-a entry after tenant-b's calls = %v, want unchanged", toolNames(again))
	}
}
