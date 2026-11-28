package scheduler

import (
	"context"
	"reflect"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

// TestTenantIsolationAgentResolver: a worker id belongs to one tenant for
// good (workercredentials refuses another tenant's create), so the colliding
// input is tenant-b's worker naming tenant-a's agent id, through both its
// credential and the legacy global agent:by-worker link.
func TestTenantIsolationAgentResolver(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	agents := store.NewAgentIdentityStoreFromClient(client)
	ctx := context.Background()
	agentA, err := agents.Create(ctx, store.AgentIdentity{TenantID: "tenant-a", Name: "agent-a-name", Owner: "owner-a", RiskTier: "high"})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := agents.LinkWorker(ctx, agentA.ID, "worker-a"); err != nil {
		t.Fatalf("link worker-a: %v", err)
	}
	if err := client.Set(ctx, "agent:by-worker:worker-b", agentA.ID, 0).Err(); err != nil {
		t.Fatalf("seed legacy link: %v", err)
	}
	resolver := NewAgentResolver(&WorkerCredentialCache{records: map[string]workercredentials.Credential{
		"worker-a": {WorkerID: "worker-a", TenantID: "tenant-a"},
		"worker-b": {WorkerID: "worker-b", TenantID: "tenant-b", AgentID: agentA.ID},
	}}, agents)
	wantA := AgentInfo{AgentID: agentA.ID, Name: "agent-a-name", RiskTier: "high"}

	if got := withoutExpiry(resolver.Resolve(ctx, "worker-a")); got != wantA {
		t.Fatalf("tenant-a Resolve = %+v, want %+v", got, wantA)
	}
	commands := mr.CommandCount()
	if got := withoutExpiry(resolver.Resolve(ctx, "worker-a")); got != wantA || mr.CommandCount() != commands {
		t.Fatalf("tenant-a repeat = %+v with %d store commands, want a cache hit with none", got, mr.CommandCount()-commands)
	}

	got := withoutExpiry(resolver.Resolve(ctx, "worker-b"))
	if want := (AgentInfo{AgentID: agentA.ID, Name: agentA.ID}); got != want {
		t.Fatalf("tenant-b Resolve = %+v, want only its own declared id %+v", got, want)
	}
	if again := withoutExpiry(resolver.Resolve(ctx, "worker-a")); again != wantA {
		t.Fatalf("tenant-a entry after tenant-b's lookup = %+v, want %+v", again, wantA)
	}
}

// TestTenantIsolationWorkerCredentialCache: tenant-b's session claims for
// tenant-a's worker carry every other value of tenant-a's credential, so only
// the tenant check in sessionClaimsMatchCredential can refuse them.
func TestTenantIsolationWorkerCredentialCache(t *testing.T) {
	credA := workercredentials.Credential{
		WorkerID: "worker-a", TenantID: "tenant-a", AgentID: "agent-a", ProofKeyID: "proof-a",
		AllowedPools: []string{"pool-a"}, AllowedTopics: []string{"job.iso"},
	}
	engine, lists := refreshTestEngine(credA, credA)
	heartbeat := &pb.Heartbeat{WorkerId: "worker-a", Pool: "pool-a"}
	claims := func(tenant string) *SessionTokenClaims {
		return &SessionTokenClaims{
			Subject: "worker-a", Tenant: tenant, AgentID: "agent-a", ProofKeyID: "proof-a",
			Audience: WorkerHandshakeAudience, SDKVersion: "node/1",
		}
	}

	if !engine.allowActiveHeartbeatPool(heartbeat, claims("tenant-a")) {
		t.Fatal("tenant-a heartbeat refused with its own credential")
	}
	before := lists.Load()
	cached, ok := engine.workerCredentialCache.Lookup("worker-a")
	if !ok || cached.TenantID != "tenant-a" || lists.Load() != before {
		t.Fatalf("Lookup = %+v, %v with %d list calls, want tenant-a's cached credential and none", cached, ok, lists.Load()-before)
	}

	if engine.allowActiveHeartbeatPool(heartbeat, claims("tenant-b")) {
		t.Fatal("tenant-b claims were accepted against tenant-a's cached credential")
	}
	after, ok := engine.workerCredentialCache.Lookup("worker-a")
	if !ok || !reflect.DeepEqual(*after, credA) {
		t.Fatalf("tenant-a credential after tenant-b's attempt = %+v, %v; want unchanged", after, ok)
	}
}

func withoutExpiry(info AgentInfo) AgentInfo {
	return AgentInfo{AgentID: info.AgentID, Name: info.Name, RiskTier: info.RiskTier}
}
