package gateway

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
	redis "github.com/redis/go-redis/v9"
)

// Standalone matcher — no Redis / no fixtures.
func TestMatchToolPattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern string
		tool    string
		want    bool
	}{
		{"cordum_install_pack", "cordum_install_pack", true},
		{"cordum_install_*", "cordum_install_pack", true},
		{"cordum_install_*", "cordum_uninstall_pack", false},
		{"cordum_*", "cordum_install_pack", true},
		{"", "cordum_install_pack", false},
		{"cordum_install_pack", "", false},
		{"*", "anything", false}, // empty-prefix glob is a footgun; refused
	}
	for _, tc := range cases {
		if got := matchToolPattern(tc.pattern, tc.tool); got != tc.want {
			t.Errorf("matchToolPattern(%q, %q) = %v, want %v", tc.pattern, tc.tool, got, tc.want)
		}
	}
}

// Full-stack: real miniredis, real AgentIdentityStore, real lookup.
func TestAgentIdentityPreapprovalLookup_RealStore(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	client := redis.NewClient(testRedisOptions(mr.Addr()))
	t.Cleanup(func() { _ = client.Close() })
	s := store.NewAgentIdentityStoreFromClient(client)
	if s == nil {
		t.Fatalf("nil store")
	}

	// Seed two identities — ci-bot has preapproved install_pack;
	// human-op has no preapprovals.
	ctx := context.Background()
	if _, err := s.Create(ctx, store.AgentIdentity{
		ID:                       "ci-bot",
		TenantID:                 "acme",
		Name:                     "CI Bot",
		Owner:                    "platform-owner",
		RiskTier:                 "medium",
		PreapprovedMutatingTools: []string{"cordum_install_pack", "cordum_create_workflow", "cordum_bulk_*"},
	}); err != nil {
		t.Fatalf("create ci-bot: %v", err)
	}
	if _, err := s.Create(ctx, store.AgentIdentity{
		ID:       "human-op",
		TenantID: "acme",
		Name:     "Human Op",
		Owner:    "platform-owner",
		RiskTier: "high",
	}); err != nil {
		t.Fatalf("create human-op: %v", err)
	}

	lookup := newAgentIdentityPreapprovalLookup(s)

	cases := []struct {
		name   string
		tenant string
		agent  string
		tool   string
		want   bool
	}{
		{"ci bot preapproved install", "acme", "ci-bot", "cordum_install_pack", true},
		{"ci bot preapproved create_workflow", "acme", "ci-bot", "cordum_create_workflow", true},
		{"ci bot preapproved trailing star", "acme", "ci-bot", "cordum_bulk_update", true},
		{"trims tenant agent and tool", " acme ", " ci-bot ", " cordum_install_pack ", true},
		{"ci bot NOT preapproved update_policy", "acme", "ci-bot", "cordum_update_policy_bundle", false},
		{"human-op never preapproved", "acme", "human-op", "cordum_install_pack", false},
		{"unknown agent → false", "acme", "not-exist", "cordum_install_pack", false},
		{"cross-tenant same agent/tool refused", "other-co", "ci-bot", "cordum_install_pack", false},
		{"empty tenant → false", "", "ci-bot", "cordum_install_pack", false},
		{"empty agent → false", "acme", "", "cordum_install_pack", false},
		{"empty tool → false", "acme", "ci-bot", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lookup.IsPreapproved(ctx, tc.tenant, tc.agent, tc.tool); got != tc.want {
				t.Errorf("IsPreapproved(%q, %q, %q) = %v, want %v", tc.tenant, tc.agent, tc.tool, got, tc.want)
			}
		})
	}
}

func TestAgentIdentityPreapprovalLookup_StoreError(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	client := redis.NewClient(testRedisOptions(mr.Addr()))
	s := store.NewAgentIdentityStoreFromClient(client)
	lookup := newAgentIdentityPreapprovalLookup(s)
	if err := client.Close(); err != nil {
		t.Fatalf("close redis client: %v", err)
	}
	if lookup.IsPreapproved(context.Background(), "tenant-a", "agent-a", "cordum_install_pack") {
		t.Fatalf("store error must fail-closed")
	}
}

func TestAgentIdentityPreapprovalLookup_NilStore(t *testing.T) {
	t.Parallel()
	lookup := newAgentIdentityPreapprovalLookup(nil)
	if lookup.IsPreapproved(context.Background(), "t", "a", "tool") {
		t.Fatalf("nil store must fail-closed")
	}
}
