package gateway

import (
	"context"
	"reflect"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestMCPIdentityFromStoreCopiesAllPublishedFields(t *testing.T) {
	src := &store.AgentIdentity{
		ID:                  "agent-prod",
		Status:              "active",
		AllowedServers:      []string{"prod-mcp"},
		AllowedTools:        []string{"repo.*"},
		AllowedResources:    []string{"cordum://repos/*"},
		Entitlements:        []string{"repo.read"},
		RiskTier:            "high",
		DataClassifications: []string{"internal"},
	}

	got := mcpIdentityFromStore(src)
	if got == nil {
		t.Fatal("mcpIdentityFromStore returned nil for active identity")
	}
	assertStringSlice(t, "AllowedServers", got.AllowedServers, []string{"prod-mcp"})
	assertStringSlice(t, "AllowedTools", got.AllowedTools, []string{"repo.*"})
	assertStringSlice(t, "AllowedResources", got.AllowedResources, []string{"cordum://repos/*"})
	assertStringSlice(t, "Entitlements", got.Entitlements, []string{"repo.read"})
	assertStringSlice(t, "DataClassifications", got.DataClassifications, []string{"internal"})
	if got.RiskTier != "high" {
		t.Fatalf("RiskTier = %q, want high", got.RiskTier)
	}

	src.AllowedServers[0] = "mutated-server"
	got.AllowedTools[0] = "mutated-tool"
	if got.AllowedServers[0] == "mutated-server" {
		t.Fatal("AllowedServers aliases store slice")
	}
	if src.AllowedTools[0] == "mutated-tool" {
		t.Fatal("AllowedTools aliases MCP slice back into store identity")
	}
}

func TestMCPIdentityFromStoreInactiveIdentitiesFailClosed(t *testing.T) {
	for _, status := range []string{"revoked", "suspended"} {
		t.Run(status, func(t *testing.T) {
			got := mcpIdentityFromStore(&store.AgentIdentity{
				ID: "agent-prod", Status: status, AllowedTools: []string{"*"},
			})
			if got != nil {
				t.Fatalf("status %q returned identity %#v, want nil", status, got)
			}
		})
	}
}

func TestMCPGateStoreBackedIdentityAllowlists(t *testing.T) {
	ctx := context.Background()
	identityStore := newMCPIdentityTestStore(t)
	allowed := createMCPStoreIdentity(t, ctx, identityStore, "agent-allow",
		[]string{"prod-mcp"}, []string{"cordum://repos/*"}, []string{"repo.read"})
	gate := actiongates.NewMCPGate(actiongates.MCPGateOptions{
		Identities:   gatewayMCPIdentityResolver{store: identityStore},
		Reachability: nil,
	})

	allowedDecision := gate.Evaluate(mcpGateAuthContext(), mcpGateInput(allowed.ID))
	requireGateDecision(t, allowedDecision, pb.DecisionType_DECISION_TYPE_ALLOW, "", "allowed")

	cases := []struct {
		name         string
		agentID      string
		servers      []string
		resources    []string
		entitlements []string
		subReason    string
	}{
		{"missing_server", "agent-no-server", nil, []string{"cordum://repos/*"}, []string{"repo.read"}, "server_not_allowlisted"},
		{"missing_resource", "agent-no-resource", []string{"prod-mcp"}, nil, []string{"repo.read"}, "resource_not_allowlisted"},
		{"missing_entitlement", "agent-no-entitlement", []string{"prod-mcp"}, []string{"cordum://repos/*"}, nil, "unlicensed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created := createMCPStoreIdentity(t, ctx, identityStore, tc.agentID, tc.servers, tc.resources, tc.entitlements)
			decision := gate.Evaluate(mcpGateAuthContext(), mcpGateInput(created.ID))
			requireGateDecision(t, decision, pb.DecisionType_DECISION_TYPE_DENY, actiongates.CodeAccessDenied, tc.subReason)
		})
	}
}

func newMCPIdentityTestStore(t *testing.T) *store.AgentIdentityStore {
	t.Helper()
	mr := miniredis.RunT(t)
	client := testredis.NewClient(t, mr.Addr())
	return store.NewAgentIdentityStoreFromClient(client)
}

func createMCPStoreIdentity(
	t *testing.T,
	ctx context.Context,
	identityStore *store.AgentIdentityStore,
	agentID string,
	servers, resources, entitlements []string,
) *store.AgentIdentity {
	t.Helper()
	created, err := identityStore.Create(ctx, store.AgentIdentity{
		ID:               agentID,
		TenantID:         "tenant-prod",
		Name:             agentID,
		Owner:            "platform",
		RiskTier:         "high",
		AllowedServers:   servers,
		AllowedTools:     []string{"repo.*"},
		AllowedResources: resources,
		Entitlements:     entitlements,
		Status:           "active",
	})
	if err != nil {
		t.Fatalf("create %s: %v", agentID, err)
	}
	return created
}

func mcpGateAuthContext() context.Context {
	return context.WithValue(context.Background(), auth.ContextKey{}, &auth.AuthContext{
		Tenant: "tenant-prod", PrincipalID: "principal", Role: "user",
	})
}

func mcpGateInput(agentID string) *config.PolicyInput {
	return &config.PolicyInput{
		Meta:   config.PolicyMeta{AgentID: agentID},
		Action: mcpGateAction(),
	}
}

func mcpGateAction() *config.ActionDescriptor {
	return &config.ActionDescriptor{
		Kind:                config.ActionKindMCPCall,
		Verb:                config.ActionVerbRead,
		Server:              "prod-mcp",
		Tool:                "repo.read",
		TargetURL:           "cordum://repos/main",
		RequiredEntitlement: "repo.read",
	}
}

func requireGateDecision(
	t *testing.T,
	got actiongates.ActionGateDecision,
	wantDecision pb.DecisionType,
	wantCode string,
	wantSubReason string,
) {
	t.Helper()
	if got.Decision != wantDecision || got.Code != wantCode || got.SubReason != wantSubReason {
		t.Fatalf("decision = (%v, %q, %q), want (%v, %q, %q)",
			got.Decision, got.Code, got.SubReason, wantDecision, wantCode, wantSubReason)
	}
}

func assertStringSlice(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
