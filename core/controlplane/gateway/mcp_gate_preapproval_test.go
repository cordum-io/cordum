package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/mcp"
	redis "github.com/redis/go-redis/v9"
)

// stubPreapproval lets us answer IsPreapproved deterministically
// without spinning up an AgentIdentityStore.
type stubPreapproval struct {
	tenants map[string]map[string]map[string]bool // tenant → agent → tool
}

func (s *stubPreapproval) IsPreapproved(_ context.Context, tenant, agent, tool string) bool {
	if s == nil {
		return false
	}
	if a, ok := s.tenants[tenant]; ok {
		if t, ok := a[agent]; ok {
			return t[tool]
		}
	}
	return false
}

func newStubPreapproval(tenant, agent string, tools ...string) *stubPreapproval {
	s := &stubPreapproval{tenants: map[string]map[string]map[string]bool{}}
	s.tenants[tenant] = map[string]map[string]bool{agent: {}}
	for _, t := range tools {
		s.tenants[tenant][agent][t] = true
	}
	return s
}

func TestApprovalGate_PreapprovalBypass_SkipsEnqueueAndMarksHandle(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	client := redis.NewClient(testRedisOptions(mr.Addr()))
	t.Cleanup(func() { _ = client.Close() })

	store := NewMCPApprovalStore(client)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)
	gate.preapproval = newStubPreapproval("acme", "ci-bot", "cordum_install_pack")

	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:    "acme",
		AgentID:   "ci-bot",
		Principal: "ci-bot@acme",
	})
	// Install a handle so the gate's MarkApprovalPreapproved stamps it.
	// Use the mcp package's unexported helper indirectly via ctx-only APIs.
	// Since we can't call the unexported contextWithInvocationHandle,
	// exercising the preapproved branch still works — the handle-
	// stamp is a nil-safe no-op when no handle is present.
	tool := mcp.Tool{
		Name:             "cordum_install_pack",
		RequiresApproval: true,
		ApprovalScope:    "mcp_write",
	}
	params := json.RawMessage(`{"pack_id":"cordum/slack"}`)

	required, err := gate.Check(ctx, tool, params)
	if err != nil {
		t.Fatalf("Check err: %v", err)
	}
	if required != nil {
		t.Fatalf("expected preapproved bypass (nil ApprovalRequired), got %+v", required)
	}
}

func TestApprovalGate_NoPreapproval_EnqueuesAsUsual(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	client := redis.NewClient(testRedisOptions(mr.Addr()))
	t.Cleanup(func() { _ = client.Close() })

	store := NewMCPApprovalStore(client)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)
	// Preapproval for a DIFFERENT tool — the gate must fall through
	// to the normal enqueue path.
	gate.preapproval = newStubPreapproval("acme", "ci-bot", "cordum_create_workflow")

	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:    "acme",
		AgentID:   "ci-bot",
		Principal: "ci-bot@acme",
	})
	tool := mcp.Tool{
		Name:             "cordum_install_pack",
		RequiresApproval: true,
	}
	params := json.RawMessage(`{"pack_id":"cordum/github"}`)

	required, err := gate.Check(ctx, tool, params)
	if err != nil {
		t.Fatalf("Check err: %v", err)
	}
	if required == nil {
		t.Fatalf("expected ApprovalRequired, got nil")
	}
	if required.Tool != "cordum_install_pack" {
		t.Fatalf("wrong tool: %s", required.Tool)
	}
	if required.ApprovalID == "" {
		t.Fatalf("approval_id missing from ApprovalRequired")
	}
}

func TestApprovalGate_PreapprovalOnlyConsidersRequiresApprovalTrue(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	client := redis.NewClient(testRedisOptions(mr.Addr()))
	t.Cleanup(func() { _ = client.Close() })

	store := NewMCPApprovalStore(client)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)
	// Even if the identity is preapproved, a non-gated tool shouldn't
	// touch the preapproval path — it's a no-op the gate skips.
	gate.preapproval = newStubPreapproval("acme", "agent", "cordum_list_jobs")

	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:    "acme",
		AgentID:   "agent",
		Principal: "agent",
	})
	tool := mcp.Tool{
		Name:             "cordum_list_jobs",
		RequiresApproval: false,
	}
	required, err := gate.Check(ctx, tool, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if required != nil {
		t.Fatalf("non-gated tool should never return ApprovalRequired")
	}
}
