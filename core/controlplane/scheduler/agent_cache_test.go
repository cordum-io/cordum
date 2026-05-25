package scheduler

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

func newTestAgentResolver(t *testing.T) (*AgentResolver, *store.AgentIdentityStore) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	agentStore := store.NewAgentIdentityStoreFromClient(client)
	credCache := &WorkerCredentialCache{
		records: map[string]workercredentials.Credential{},
	}
	resolver := NewAgentResolver(credCache, agentStore)
	return resolver, agentStore
}

func TestAgentResolver_LinkedWorker(t *testing.T) {
	resolver, agentStore := newTestAgentResolver(t)
	ctx := context.Background()

	agent, err := agentStore.Create(ctx, store.AgentIdentity{
		Name:     "test-agent",
		Owner:    "admin",
		RiskTier: "high",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// Populate credential cache with a linked worker.
	resolver.credCache.mu.Lock()
	resolver.credCache.records["worker-linked"] = workercredentials.Credential{
		WorkerID: "worker-linked",
		AgentID:  agent.ID,
	}
	resolver.credCache.mu.Unlock()

	info := resolver.Resolve(ctx, "worker-linked")
	if info.AgentID != agent.ID {
		t.Errorf("AgentID = %q, want %q", info.AgentID, agent.ID)
	}
	if info.Name != "test-agent" {
		t.Errorf("Name = %q, want %q", info.Name, "test-agent")
	}
	if info.RiskTier != "high" {
		t.Errorf("RiskTier = %q, want %q", info.RiskTier, "high")
	}
}

// TestAgentResolver_CAPNameFallbackAndSpoofProtection proves the task-c8d4b056
// resolution contract: a self-reported CAP display label fills the agent name
// for an UNLINKED worker, but can NEVER override an authenticated Agent Identity
// name (spoof protection). The label flows in via the registry's AgentName
// accessor, exactly as the production wiring would inject it.
func TestAgentResolver_CAPNameFallbackAndSpoofProtection(t *testing.T) {
	resolver, agentStore := newTestAgentResolver(t)
	ctx := context.Background()

	reg := NewMemoryRegistry()
	t.Cleanup(reg.Close)
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-cap", AgentName: "Billing Bot"})
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-auth", AgentName: "Spoofed Privileged Agent"})
	resolver.WithCAPNameResolver(reg.AgentName)

	// (1) Unlinked worker with a CAP label: the self-reported label fills the
	// display name, but AgentID stays "unlinked" — the label is never mistaken
	// for an authenticated identity.
	info := resolver.Resolve(ctx, "worker-cap")
	if info.AgentID != agentCacheUnlinked {
		t.Errorf("unlinked AgentID = %q, want %q", info.AgentID, agentCacheUnlinked)
	}
	if info.Name != "Billing Bot" {
		t.Errorf("CAP fallback Name = %q, want %q", info.Name, "Billing Bot")
	}

	// (2) Authenticated identity must win over a spoofed CAP label for the same
	// worker.
	agent, err := agentStore.Create(ctx, store.AgentIdentity{
		Name:     "Authentic Agent",
		Owner:    "admin",
		RiskTier: "high",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	resolver.credCache.mu.Lock()
	resolver.credCache.records["worker-auth"] = workercredentials.Credential{
		WorkerID: "worker-auth",
		AgentID:  agent.ID,
	}
	resolver.credCache.mu.Unlock()

	info = resolver.Resolve(ctx, "worker-auth")
	if info.AgentID != agent.ID {
		t.Errorf("auth AgentID = %q, want %q", info.AgentID, agent.ID)
	}
	if info.Name != "Authentic Agent" {
		t.Errorf("spoof protection failed: Name = %q, want authenticated %q (CAP label must not override)", info.Name, "Authentic Agent")
	}
}

func TestAgentResolver_UnlinkedWorker(t *testing.T) {
	resolver, _ := newTestAgentResolver(t)
	ctx := context.Background()

	// Credential exists but has no agent_id.
	resolver.credCache.mu.Lock()
	resolver.credCache.records["worker-legacy"] = workercredentials.Credential{
		WorkerID: "worker-legacy",
	}
	resolver.credCache.mu.Unlock()

	info := resolver.Resolve(ctx, "worker-legacy")
	if info.AgentID != agentCacheUnlinked {
		t.Errorf("AgentID = %q, want %q", info.AgentID, agentCacheUnlinked)
	}
	if info.Name != agentCacheUnlinked {
		t.Errorf("Name = %q, want %q", info.Name, agentCacheUnlinked)
	}
}

func TestAgentResolver_UnknownWorker(t *testing.T) {
	resolver, _ := newTestAgentResolver(t)
	ctx := context.Background()

	info := resolver.Resolve(ctx, "worker-unknown")
	if info.AgentID != agentCacheUnlinked {
		t.Errorf("AgentID = %q, want %q", info.AgentID, agentCacheUnlinked)
	}
}

func TestAgentResolver_EmptyWorkerID(t *testing.T) {
	resolver, _ := newTestAgentResolver(t)
	info := resolver.Resolve(context.Background(), "")
	if info.AgentID != agentCacheUnlinked {
		t.Errorf("AgentID = %q, want %q", info.AgentID, agentCacheUnlinked)
	}
}

func TestAgentResolver_NilResolver(t *testing.T) {
	var resolver *AgentResolver
	info := resolver.Resolve(context.Background(), "worker-1")
	if info.AgentID != agentCacheUnlinked {
		t.Errorf("AgentID = %q, want %q", info.AgentID, agentCacheUnlinked)
	}
}

func TestAgentResolver_CacheHit(t *testing.T) {
	resolver, agentStore := newTestAgentResolver(t)
	ctx := context.Background()

	agent, err := agentStore.Create(ctx, store.AgentIdentity{
		Name:     "cached-agent",
		Owner:    "admin",
		RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	resolver.credCache.mu.Lock()
	resolver.credCache.records["worker-cached"] = workercredentials.Credential{
		WorkerID: "worker-cached",
		AgentID:  agent.ID,
	}
	resolver.credCache.mu.Unlock()

	// First call populates cache.
	info1 := resolver.Resolve(ctx, "worker-cached")
	if info1.AgentID != agent.ID {
		t.Fatalf("first resolve: AgentID = %q, want %q", info1.AgentID, agent.ID)
	}

	// Verify entry exists in cache.
	resolver.mu.RLock()
	_, cached := resolver.cache["worker-cached"]
	resolver.mu.RUnlock()
	if !cached {
		t.Fatal("expected entry in cache after first resolve")
	}

	// Second call should hit cache with same result.
	info2 := resolver.Resolve(ctx, "worker-cached")
	if info2.AgentID != info1.AgentID {
		t.Errorf("cached resolve mismatch: %q != %q", info2.AgentID, info1.AgentID)
	}
	if info2.Name != info1.Name {
		t.Errorf("cached name mismatch: %q != %q", info2.Name, info1.Name)
	}
}

func TestAgentResolver_CacheTTLExpiry(t *testing.T) {
	resolver, agentStore := newTestAgentResolver(t)
	ctx := context.Background()

	agent, err := agentStore.Create(ctx, store.AgentIdentity{
		Name: "ttl-agent", Owner: "admin", RiskTier: "high",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	resolver.credCache.mu.Lock()
	resolver.credCache.records["worker-ttl"] = workercredentials.Credential{
		WorkerID: "worker-ttl", AgentID: agent.ID,
	}
	resolver.credCache.mu.Unlock()

	// Populate cache.
	info := resolver.Resolve(ctx, "worker-ttl")
	if info.AgentID != agent.ID {
		t.Fatalf("initial resolve: AgentID = %q, want %q", info.AgentID, agent.ID)
	}

	// Manually expire the cache entry by setting ExpiresAt in the past.
	resolver.mu.Lock()
	if entry, ok := resolver.cache["worker-ttl"]; ok {
		entry.ExpiresAt = time.Now().Add(-1 * time.Second)
		resolver.cache["worker-ttl"] = entry
	}
	resolver.mu.Unlock()

	// Next resolve should miss cache (expired) and re-fetch from store.
	info2 := resolver.Resolve(ctx, "worker-ttl")
	if info2.AgentID != agent.ID {
		t.Fatalf("post-expiry resolve: AgentID = %q, want %q", info2.AgentID, agent.ID)
	}
}
