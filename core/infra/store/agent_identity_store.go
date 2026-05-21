package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	agentIdentityKeyPrefix = "agent:identity:"
	agentIdentityIndexKey  = "agent:identity:index"
	agentByWorkerKeyPrefix = "agent:by-worker:"
	defaultAgentListLimit  = 50
	maxAgentListLimit      = 200
)

var validRiskTiers = map[string]bool{
	"low":      true,
	"medium":   true,
	"high":     true,
	"critical": true,
}

var validAgentStatuses = map[string]bool{
	"active":    true,
	"suspended": true,
	"revoked":   true,
}

// AgentIdentity is the canonical agent identity resource stored in Redis.
type AgentIdentity struct {
	ID                       string   `json:"id"`
	TenantID                 string   `json:"tenant_id,omitempty"`
	Name                     string   `json:"name"`
	Description              string   `json:"description,omitempty"`
	Owner                    string   `json:"owner"`
	Team                     string   `json:"team,omitempty"`
	RiskTier                 string   `json:"risk_tier"`
	AllowedTopics            []string `json:"allowed_topics,omitempty"`
	AllowedPools             []string `json:"allowed_pools,omitempty"`
	AllowedServers           []string `json:"allowed_servers,omitempty"`
	AllowedTools             []string `json:"allowed_tools,omitempty"`
	AllowedResources         []string `json:"allowed_resources,omitempty"`
	Entitlements             []string `json:"entitlements,omitempty"`
	PreapprovedMutatingTools []string `json:"preapproved_mutating_tools,omitempty"`
	DataClassifications      []string `json:"data_classifications,omitempty"`
	Status                   string   `json:"status"`
	CreatedAt                string   `json:"created_at"`
	UpdatedAt                string   `json:"updated_at"`
}

// AgentIdentityFilter controls list filtering.
type AgentIdentityFilter struct {
	Status   string
	RiskTier string
	Team     string
}

// AgentIdentityStore manages agent identity resources backed by Redis.
type AgentIdentityStore struct {
	client redis.UniversalClient
}

// NewAgentIdentityStore constructs a Redis-backed agent identity store.
func NewAgentIdentityStore(url string) (*AgentIdentityStore, error) {
	if url == "" {
		url = defaultRedisURL
	}
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("agent identity store: parse redis url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("agent identity store: connect redis: %w", err)
	}
	slog.Debug("agent identity store connected", "component", "store")
	return &AgentIdentityStore{client: client}, nil
}

// NewAgentIdentityStoreFromClient constructs a store from an existing Redis client.
// Used in tests and when sharing a Redis connection.
func NewAgentIdentityStoreFromClient(client redis.UniversalClient) *AgentIdentityStore {
	if client == nil {
		return nil
	}
	return &AgentIdentityStore{client: client}
}

// Create validates and stores a new agent identity.
func (s *AgentIdentityStore) Create(ctx context.Context, identity AgentIdentity) (*AgentIdentity, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("agent_identity_store: redis client not initialized")
	}
	identity = normalizeAgentIdentity(identity)

	if identity.ID == "" {
		identity.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	identity.CreatedAt = now.Format(time.RFC3339Nano)
	identity.UpdatedAt = identity.CreatedAt
	if identity.Status == "" {
		identity.Status = "active"
	}

	if err := validateAgentIdentity(identity); err != nil {
		return nil, err
	}

	key := agentIdentityKeyPrefix + identity.ID
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("marshal agent identity: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, key, data, 0)
	pipe.ZAdd(ctx, agentIdentityIndexKey, redis.Z{
		Score:  float64(now.UnixNano()),
		Member: identity.ID,
	})
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("create agent identity: %w", err)
	}

	return &identity, nil
}

// Get returns the agent identity by ID scoped to tenantID, or nil if not
// found OR if the stored TenantID does not match. An empty tenantID bypasses
// the tenant check and is reserved for internal/system lookups (audit
// enrichment, worker handshake) where the caller is not a tenant principal.
// Returning nil (not a forbidden error) on mismatch prevents an existence
// oracle for cross-tenant ID guesses.
func (s *AgentIdentityStore) Get(ctx context.Context, tenantID, id string) (*AgentIdentity, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("agent_identity_store: redis client not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("agent identity id required")
	}

	data, err := s.client.Get(ctx, agentIdentityKeyPrefix+id).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get agent identity %s: %w", id, err)
	}

	var identity AgentIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, fmt.Errorf("unmarshal agent identity %s: %w", id, err)
	}
	identity = normalizeAgentIdentity(identity)
	if tenantID = strings.TrimSpace(tenantID); tenantID != "" {
		if strings.TrimSpace(identity.TenantID) != tenantID {
			return nil, nil
		}
	}
	return &identity, nil
}

// List returns tenant-scoped agent identities with cursor-based pagination and optional filtering.
func (s *AgentIdentityStore) List(ctx context.Context, tenantID, cursor string, limit int, filter AgentIdentityFilter) ([]*AgentIdentity, string, error) {
	if s == nil || s.client == nil {
		return nil, "", fmt.Errorf("agent_identity_store: redis client not initialized")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, "", fmt.Errorf("agent identity tenant id required")
	}
	if limit <= 0 {
		limit = defaultAgentListLimit
	}
	if limit > maxAgentListLimit {
		limit = maxAgentListLimit
	}

	// Cursor format: "score:offset" — score is the sorted-set score to resume
	// from (inclusive), offset is the number of items at that score already
	// consumed across previous pages. ZRANGEBYSCORE with min=score and
	// Offset=N skips the first N members with score >= min.
	//
	// When a page crosses into a higher score bucket, the offset resets to
	// the count of items consumed at that new score (since the new min shifts
	// past all lower-scored members). When staying in the same bucket, the
	// offset accumulates across pages.
	minScore := "-inf"
	var resumeOffset int64
	var cursorScoreF float64
	hasCursorScore := false
	if cursor != "" {
		if idx := strings.IndexByte(cursor, ':'); idx > 0 {
			minScore = cursor[:idx]
			if v, err := strconv.ParseFloat(minScore, 64); err == nil {
				cursorScoreF = v
				hasCursorScore = true
			}
			off, err := strconv.ParseInt(cursor[idx+1:], 10, 64)
			if err == nil && off > 0 {
				resumeOffset = off
			}
		}
	}

	// Scan the sorted set iteratively, fetching in batches. Filtering happens
	// after fetch, so we may need multiple batches to fill the result page
	// when many entries don't match the filter.
	const maxBatchSize int64 = 100
	batchSize := max(int64(limit*3), 30)
	if batchSize > maxBatchSize {
		batchSize = maxBatchSize
	}

	var results []*AgentIdentity
	var lastScore float64
	var itemsAtLastScore int64
	processedAny := false
	scanOffset := resumeOffset
	scanMin := minScore

	for len(results) < limit {
		members, err := s.client.ZRangeByScoreWithScores(ctx, agentIdentityIndexKey, &redis.ZRangeBy{
			Min:    scanMin,
			Max:    "+inf",
			Offset: scanOffset,
			Count:  batchSize,
		}).Result()
		if err != nil {
			return nil, "", fmt.Errorf("list agent identities: %w", err)
		}
		if len(members) == 0 {
			break // exhausted the sorted set
		}

		processedInBatch := 0
		for _, z := range members {
			if len(results) >= limit {
				break
			}
			processedInBatch++
			processedAny = true
			// Track items consumed per score bucket. Reset when crossing into
			// a new score so the next-page offset is scoped to that bucket.
			if z.Score != lastScore {
				lastScore = z.Score
				itemsAtLastScore = 0
			}
			itemsAtLastScore++

			id, ok := z.Member.(string)
			if !ok {
				continue
			}
			identity, err := s.Get(ctx, tenantID, id)
			if err != nil {
				slog.Warn("list agent identities: skip unreadable entry", "id", id, "error", err)
				continue
			}
			if identity == nil {
				continue
			}
			if !matchesAgentFilter(identity, filter) {
				continue
			}
			results = append(results, identity)
		}

		if processedInBatch == 0 {
			break
		}
		// Advance internal scanning by only the members this call actually
		// processed. A fetched Redis batch may contain entries beyond the
		// requested page limit; using len(members) here would skip those
		// unprocessed records on the next page.
		scanOffset += int64(processedInBatch)
	}

	// Build the next-page cursor.
	//
	// Two cases:
	// (a) lastScore == cursor's score: we stayed in the same score bucket,
	//     so offset must accumulate (resumeOffset + items consumed this page).
	// (b) lastScore > cursor's score (or no cursor): we crossed into a new
	//     bucket, so offset is just items consumed at lastScore on this page.
	//     The new min=lastScore already skips everything below.
	nextCursorStr := ""
	if len(results) >= limit && processedAny {
		var nextOffset int64
		if hasCursorScore && lastScore == cursorScoreF {
			nextOffset = resumeOffset + itemsAtLastScore
		} else {
			nextOffset = itemsAtLastScore
		}
		nextCursorStr = strconv.FormatFloat(lastScore, 'f', -1, 64) + ":" + strconv.FormatInt(nextOffset, 10)
	}

	return results, nextCursorStr, nil
}

// Update applies partial updates to an existing agent identity scoped to
// tenantID. An empty tenantID bypasses the tenant check. When tenantID is
// set and the stored identity's TenantID differs, Update returns a "not
// found" error — the same error class as a missing record — to avoid an
// existence oracle for cross-tenant probes.
func (s *AgentIdentityStore) Update(ctx context.Context, tenantID, id string, updates AgentIdentity) (*AgentIdentity, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("agent_identity_store: redis client not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("agent identity id required")
	}

	existing, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("agent identity not found")
	}

	if v := strings.TrimSpace(updates.Name); v != "" {
		existing.Name = v
	}
	if v := strings.TrimSpace(updates.Description); v != "" {
		existing.Description = v
	}
	if v := strings.TrimSpace(updates.Owner); v != "" {
		existing.Owner = v
	}
	if v := strings.TrimSpace(updates.Team); v != "" {
		existing.Team = v
	}
	if v := strings.TrimSpace(updates.RiskTier); v != "" {
		existing.RiskTier = v
	}
	if v := strings.TrimSpace(updates.Status); v != "" {
		existing.Status = v
	}
	if updates.AllowedTopics != nil {
		existing.AllowedTopics = updates.AllowedTopics
	}
	if updates.AllowedPools != nil {
		existing.AllowedPools = updates.AllowedPools
	}
	if updates.AllowedServers != nil {
		existing.AllowedServers = updates.AllowedServers
	}
	if updates.AllowedTools != nil {
		existing.AllowedTools = updates.AllowedTools
	}
	if updates.AllowedResources != nil {
		existing.AllowedResources = updates.AllowedResources
	}
	if updates.Entitlements != nil {
		existing.Entitlements = updates.Entitlements
	}
	if updates.PreapprovedMutatingTools != nil {
		existing.PreapprovedMutatingTools = updates.PreapprovedMutatingTools
	}
	if updates.DataClassifications != nil {
		existing.DataClassifications = updates.DataClassifications
	}

	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	*existing = normalizeAgentIdentity(*existing)

	if err := validateAgentIdentity(*existing); err != nil {
		return nil, err
	}

	data, err := json.Marshal(existing)
	if err != nil {
		return nil, fmt.Errorf("marshal agent identity: %w", err)
	}
	if err := s.client.Set(ctx, agentIdentityKeyPrefix+id, data, 0).Err(); err != nil {
		return nil, fmt.Errorf("update agent identity: %w", err)
	}
	return existing, nil
}

// Delete soft-deletes an agent identity by setting status to "revoked",
// scoped to tenantID. An empty tenantID bypasses the tenant check. When
// tenantID is set and the stored identity's TenantID differs, Delete
// returns a "not found" error — the same error class as a missing record
// — to avoid an existence oracle for cross-tenant probes.
func (s *AgentIdentityStore) Delete(ctx context.Context, tenantID, id string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("agent_identity_store: redis client not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("agent identity id required")
	}

	existing, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("agent identity not found")
	}

	existing.Status = "revoked"
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)

	data, err := json.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshal agent identity: %w", err)
	}
	if err := s.client.Set(ctx, agentIdentityKeyPrefix+id, data, 0).Err(); err != nil {
		return fmt.Errorf("delete agent identity: %w", err)
	}
	return nil
}

// GetByWorkerID returns the agent identity linked to the given worker ID, or nil if unlinked.
func (s *AgentIdentityStore) GetByWorkerID(ctx context.Context, workerID string) (*AgentIdentity, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("agent_identity_store: redis client not initialized")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("worker id required")
	}

	agentID, err := s.client.Get(ctx, agentByWorkerKeyPrefix+workerID).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get agent by worker %s: %w", workerID, err)
	}
	return s.Get(ctx, "", agentID)
}

// LinkWorker creates a reverse-lookup mapping from worker ID to agent identity ID.
func (s *AgentIdentityStore) LinkWorker(ctx context.Context, agentID, workerID string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("agent_identity_store: redis client not initialized")
	}
	agentID = strings.TrimSpace(agentID)
	workerID = strings.TrimSpace(workerID)
	if agentID == "" || workerID == "" {
		return fmt.Errorf("agent id and worker id required")
	}
	if err := s.client.Set(ctx, agentByWorkerKeyPrefix+workerID, agentID, 0).Err(); err != nil {
		return fmt.Errorf("link worker %s -> %s: %w", workerID, agentID, err)
	}
	return nil
}

// UnlinkWorker removes the reverse-lookup mapping for a worker ID.
func (s *AgentIdentityStore) UnlinkWorker(ctx context.Context, workerID string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("agent_identity_store: redis client not initialized")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return fmt.Errorf("worker id required")
	}
	if err := s.client.Del(ctx, agentByWorkerKeyPrefix+workerID).Err(); err != nil {
		return fmt.Errorf("unlink worker %s: %w", workerID, err)
	}
	return nil
}

// Close closes the underlying Redis client.
func (s *AgentIdentityStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func validateAgentIdentity(a AgentIdentity) error {
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("agent identity name required")
	}
	if strings.TrimSpace(a.Owner) == "" {
		return fmt.Errorf("agent identity owner required")
	}
	if !validRiskTiers[strings.ToLower(strings.TrimSpace(a.RiskTier))] {
		return fmt.Errorf("agent identity risk_tier must be one of: low, medium, high, critical")
	}
	if !validAgentStatuses[strings.ToLower(strings.TrimSpace(a.Status))] {
		return fmt.Errorf("agent identity status must be one of: active, suspended, revoked")
	}
	if err := parseRFC3339Any(a.CreatedAt); err != nil {
		return fmt.Errorf("agent identity created_at must be RFC3339: %w", err)
	}
	if err := parseRFC3339Any(a.UpdatedAt); err != nil {
		return fmt.Errorf("agent identity updated_at must be RFC3339: %w", err)
	}
	return nil
}

func normalizeAgentIdentity(a AgentIdentity) AgentIdentity {
	a.ID = strings.TrimSpace(a.ID)
	a.TenantID = strings.TrimSpace(a.TenantID)
	a.Name = strings.TrimSpace(a.Name)
	a.Description = strings.TrimSpace(a.Description)
	a.Owner = strings.TrimSpace(a.Owner)
	a.Team = strings.TrimSpace(a.Team)
	a.RiskTier = strings.ToLower(strings.TrimSpace(a.RiskTier))
	a.Status = strings.ToLower(strings.TrimSpace(a.Status))
	a.AllowedTopics = normalizeStringSlice(a.AllowedTopics)
	a.AllowedPools = normalizeStringSlice(a.AllowedPools)
	a.AllowedServers = normalizeStringSlice(a.AllowedServers)
	a.AllowedTools = normalizeStringSlice(a.AllowedTools)
	a.AllowedResources = normalizeStringSlice(a.AllowedResources)
	a.Entitlements = normalizeStringSlice(a.Entitlements)
	a.PreapprovedMutatingTools = normalizeStringSlice(a.PreapprovedMutatingTools)
	a.DataClassifications = normalizeStringSlice(a.DataClassifications)
	return a
}

func normalizeStringSlice(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

// parseRFC3339Any accepts both time.RFC3339 and time.RFC3339Nano formats.
func parseRFC3339Any(s string) error {
	if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
		if _, err2 := time.Parse(time.RFC3339, s); err2 != nil {
			return err2
		}
	}
	return nil
}

func matchesAgentFilter(a *AgentIdentity, f AgentIdentityFilter) bool {
	if f.Status != "" && !strings.EqualFold(a.Status, f.Status) {
		return false
	}
	if f.RiskTier != "" && !strings.EqualFold(a.RiskTier, f.RiskTier) {
		return false
	}
	if f.Team != "" && !strings.EqualFold(a.Team, f.Team) {
		return false
	}
	return true
}
