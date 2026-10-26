package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// LegalHold — per-tenant immutable retention flag
// ---------------------------------------------------------------------------

const (
	legalHoldKeyPrefix = "audit:legal_hold:"
	legalHoldSetPrefix = "audit:legal_holds:"
)

// LegalHold represents an active or released legal hold on audit data.
type LegalHold struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Reason     string     `json:"reason"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
	ReleasedBy string     `json:"released_by,omitempty"`
}

// IsActive returns true if the hold has not been released.
func (h *LegalHold) IsActive() bool {
	return h != nil && h.ReleasedAt == nil
}

// ---------------------------------------------------------------------------
// LegalHoldStore — Redis-backed legal hold storage
// ---------------------------------------------------------------------------

// LegalHoldStore manages legal holds on audit data in Redis. Every write
// touches one key, so it runs on Redis Cluster too.
type LegalHoldStore struct {
	client redis.UniversalClient
}

// NewLegalHoldStore connects a legal hold store with its own client, built
// like every Cordum Redis client (REDIS_CLUSTER_ADDRESSES selects a cluster).
func NewLegalHoldStore(redisURL string) (*LegalHoldStore, error) {
	client, err := redisutil.NewClient(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &LegalHoldStore{client: client}, nil
}

// NewLegalHoldStoreFromClient creates a LegalHoldStore on an existing client,
// such as the gateway's shared one. Do not Close it then.
func NewLegalHoldStoreFromClient(client redis.UniversalClient) *LegalHoldStore {
	return &LegalHoldStore{client: client}
}

// CreateHold creates a new legal hold on a tenant's audit data.
func (s *LegalHoldStore) CreateHold(ctx context.Context, tenantID, reason, createdBy string) (*LegalHold, error) {
	tenantID = strings.TrimSpace(tenantID)
	reason = strings.TrimSpace(reason)
	createdBy = strings.TrimSpace(createdBy)

	if tenantID == "" {
		return nil, fmt.Errorf("tenant_id required")
	}
	if reason == "" {
		return nil, fmt.Errorf("reason required")
	}

	// Check for existing active hold on this tenant
	active, err := s.activeHoldForTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if active != nil {
		return nil, ErrHoldAlreadyExists
	}

	hold := &LegalHold{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Reason:    reason,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}

	data, err := json.Marshal(hold)
	if err != nil {
		return nil, fmt.Errorf("marshal hold: %w", err)
	}

	// The tenant index and the hold record hash to different Redis Cluster
	// slots, so they are written one at a time, index first: a failure in
	// between leaves an index entry without a record, which every reader
	// skips (no hold), rather than a record ListHolds("") would show as an
	// active hold that IsUnderHold never sees.
	if err := s.client.SAdd(ctx, legalHoldSetPrefix+tenantID, hold.ID).Err(); err != nil {
		return nil, fmt.Errorf("redis create hold: %w", err)
	}
	if err := s.client.Set(ctx, legalHoldKeyPrefix+hold.ID, data, 0).Err(); err != nil {
		return nil, fmt.Errorf("redis create hold: %w", err)
	}

	return hold, nil
}

// GetHold retrieves a legal hold by ID.
func (s *LegalHoldStore) GetHold(ctx context.Context, id string) (*LegalHold, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrHoldNotFound
	}
	data, err := s.client.Get(ctx, legalHoldKeyPrefix+id).Bytes()
	if err == redis.Nil {
		return nil, ErrHoldNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get hold: %w", err)
	}
	var hold LegalHold
	if err := json.Unmarshal(data, &hold); err != nil {
		return nil, fmt.Errorf("unmarshal hold: %w", err)
	}
	return &hold, nil
}

// ListHolds returns all holds for a tenant (active and released).
// If tenant is empty, returns holds for all tenants.
func (s *LegalHoldStore) ListHolds(ctx context.Context, tenant string) ([]*LegalHold, error) {
	tenant = strings.TrimSpace(tenant)

	var ids []string
	if tenant != "" {
		var err error
		ids, err = s.client.SMembers(ctx, legalHoldSetPrefix+tenant).Result()
		if err != nil {
			return nil, fmt.Errorf("redis smembers holds: %w", err)
		}
	} else {
		// Scan all legal hold keys
		var cursor uint64
		for {
			keys, next, err := s.client.Scan(ctx, cursor, legalHoldKeyPrefix+"*", 100).Result()
			if err != nil {
				return nil, fmt.Errorf("redis scan holds: %w", err)
			}
			for _, key := range keys {
				ids = append(ids, strings.TrimPrefix(key, legalHoldKeyPrefix))
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}

	if len(ids) == 0 {
		return []*LegalHold{}, nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.Get(ctx, legalHoldKeyPrefix+id)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("redis pipeline holds: %w", err)
	}

	holds := make([]*LegalHold, 0, len(ids))
	for _, cmd := range cmds {
		data, err := cmd.Bytes()
		if err != nil {
			continue
		}
		var hold LegalHold
		if err := json.Unmarshal(data, &hold); err != nil {
			continue
		}
		holds = append(holds, &hold)
	}
	return holds, nil
}

// ReleaseHold releases a legal hold. Does NOT delete retained data.
func (s *LegalHoldStore) ReleaseHold(ctx context.Context, id, releasedBy string) error {
	id = strings.TrimSpace(id)
	releasedBy = strings.TrimSpace(releasedBy)
	if id == "" {
		return ErrHoldNotFound
	}

	hold, err := s.GetHold(ctx, id)
	if err != nil {
		return err
	}
	if !hold.IsActive() {
		return ErrHoldAlreadyReleased
	}

	now := time.Now().UTC()
	hold.ReleasedAt = &now
	hold.ReleasedBy = releasedBy

	data, err := json.Marshal(hold)
	if err != nil {
		return fmt.Errorf("marshal hold: %w", err)
	}

	if err := s.client.Set(ctx, legalHoldKeyPrefix+id, data, 0).Err(); err != nil {
		return fmt.Errorf("redis update hold: %w", err)
	}
	return nil
}

// IsUnderHold returns true if the tenant has any active (unreleased) legal hold.
func (s *LegalHoldStore) IsUnderHold(ctx context.Context, tenant string) (bool, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return false, nil
	}
	hold, err := s.activeHoldForTenant(ctx, tenant)
	if err != nil {
		return false, err
	}
	return hold != nil, nil
}

// activeHoldForTenant finds the first active hold for a tenant.
func (s *LegalHoldStore) activeHoldForTenant(ctx context.Context, tenant string) (*LegalHold, error) {
	ids, err := s.client.SMembers(ctx, legalHoldSetPrefix+tenant).Result()
	if err != nil {
		return nil, fmt.Errorf("redis smembers holds: %w", err)
	}
	for _, id := range ids {
		hold, err := s.GetHold(ctx, id)
		if errors.Is(err, ErrHoldNotFound) {
			continue // dangling index entry
		}
		if err != nil {
			// Surface read/decode failures: treating an unreadable hold as
			// "not held" would let retention delete held data.
			return nil, err
		}
		if hold.IsActive() {
			return hold, nil
		}
	}
	return nil, nil
}

// Close closes the store's Redis client: only for a store from
// NewLegalHoldStore.
func (s *LegalHoldStore) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	ErrHoldNotFound        = fmt.Errorf("legal hold not found")
	ErrHoldAlreadyExists   = fmt.Errorf("active legal hold already exists for this tenant")
	ErrHoldAlreadyReleased = fmt.Errorf("legal hold already released")
)
