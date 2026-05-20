package scheduler

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/store"
)

const (
	agentCacheTTL      = 30 * time.Second
	agentCacheUnlinked = "unlinked"
)

// AgentInfo holds resolved agent identity fields for audit events.
type AgentInfo struct {
	AgentID   string
	Name      string
	RiskTier  string
	ExpiresAt time.Time
}

// AgentResolver resolves worker IDs to agent identity information.
// It uses the credential cache for worker→agent_id mapping and the agent
// identity store for agent_id→details, with an in-memory TTL cache to
// avoid per-job Redis lookups.
type AgentResolver struct {
	credCache  *WorkerCredentialCache
	agentStore *store.AgentIdentityStore

	mu    sync.RWMutex
	cache map[string]AgentInfo // keyed by worker_id
}

// NewAgentResolver creates a resolver. Both parameters are optional — a nil
// credCache or agentStore results in all lookups returning "unlinked".
func NewAgentResolver(credCache *WorkerCredentialCache, agentStore *store.AgentIdentityStore) *AgentResolver {
	return &AgentResolver{
		credCache:  credCache,
		agentStore: agentStore,
		cache:      make(map[string]AgentInfo),
	}
}

// Resolve returns agent identity info for the given worker ID.
// Returns a cached entry if available and not expired; otherwise performs
// a lookup through credential cache → agent identity store.
// Legacy workers without an agent_id return AgentInfo with AgentID="unlinked".
func (r *AgentResolver) Resolve(ctx context.Context, workerID string) AgentInfo {
	if r == nil {
		return unlinkedAgent()
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return unlinkedAgent()
	}

	// Check cache under read lock.
	r.mu.RLock()
	if entry, ok := r.cache[workerID]; ok && time.Now().Before(entry.ExpiresAt) {
		r.mu.RUnlock()
		return entry
	}
	r.mu.RUnlock()

	// Cache miss — resolve and cache under write lock.
	info := r.resolveFromStores(ctx, workerID)
	info.ExpiresAt = time.Now().Add(agentCacheTTL)

	r.mu.Lock()
	r.cache[workerID] = info
	r.mu.Unlock()

	return info
}

func (r *AgentResolver) resolveFromStores(ctx context.Context, workerID string) AgentInfo {
	// Prefer the canonical reverse-lookup in the agent identity store when it
	// is available. Newer worker credentials no longer carry an embedded
	// agent_id, so the store link is the source of truth.
	if r.agentStore != nil {
		if identity, err := r.agentStore.GetByWorkerID(ctx, workerID); err == nil && identity != nil {
			return AgentInfo{
				AgentID:  identity.ID,
				Name:     identity.Name,
				RiskTier: identity.RiskTier,
			}
		}
	}

	// Fall back to the legacy worker-credential mapping when present.
	agentID := r.agentIDFromCredential(workerID)
	if agentID == "" {
		return unlinkedAgent()
	}

	if r.agentStore == nil {
		return AgentInfo{AgentID: agentID, Name: agentID, RiskTier: ""}
	}

	identity, err := r.agentStore.Get(ctx, "", agentID)
	if err != nil || identity == nil {
		return AgentInfo{AgentID: agentID, Name: agentID, RiskTier: ""}
	}

	return AgentInfo{AgentID: identity.ID, Name: identity.Name, RiskTier: identity.RiskTier}
}

func (r *AgentResolver) agentIDFromCredential(workerID string) string {
	if r.credCache == nil {
		return ""
	}
	r.credCache.mu.RLock()
	cred, ok := r.credCache.records[workerID]
	r.credCache.mu.RUnlock()
	if !ok {
		return ""
	}
	return strings.TrimSpace(cred.AgentID)
}

func unlinkedAgent() AgentInfo {
	return AgentInfo{
		AgentID:  agentCacheUnlinked,
		Name:     agentCacheUnlinked,
		RiskTier: "",
	}
}
