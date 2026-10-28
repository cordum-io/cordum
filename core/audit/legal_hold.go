package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
	// legalHoldInflightPrefix + tenant is a sorted set of the tenant's
	// deletions in progress (DeleteUnlessHeld), "<attempt id> <key>" scored
	// by Redis time in unix ms. CreateHold returns only once the deletions
	// registered before its fence have ended.
	legalHoldInflightPrefix = "audit:legal_hold_inflight:"
	// legalHoldFenceSuffix ends the fence key of a tenant's chain stream
	// (legalHoldFenceKey). The fence hashes to the stream's slot, so the trim
	// scripts check it in the same atomic step that deletes.
	legalHoldFenceSuffix = ":legal_hold_fence"
)

var (
	// legalHoldInflightTTL drops the registrations a crashed sweeper left.
	legalHoldInflightTTL = 24 * time.Hour
	// legalHoldInflightStale is how long a registered deletion counts as in
	// flight. It bounds how long CreateHold waits behind a crashed sweeper;
	// one deletion (one Edge session) takes well under it.
	// ponytail: a deletion that outlasts this is no longer waited for;
	// heartbeat the registration if deletions ever grow that long.
	legalHoldInflightStale = 2 * time.Minute
	// legalHoldInflightPoll is how often CreateHold re-reads the registry.
	legalHoldInflightPoll = 10 * time.Millisecond

	// newHoldID mints hold IDs; tests pin it.
	newHoldID = uuid.NewString
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
//
// A hold blocks deletion in two layers. Every deletion path reads the hold
// records first (IsUnderHold). To close the gap between that read and the
// delete, CreateHold writes a fence key for the tenant's chain stream before
// it returns: the audit trim scripts refuse while the fence exists, in the
// atomic step that would delete. Multi-key deletions register themselves
// (DeleteUnlessHeld), and CreateHold waits for the ones registered before
// its fence. Once CreateHold returns, no deletion of the tenant's data
// completes.
type LegalHoldStore struct {
	client      redis.UniversalClient
	chainPrefix string
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
	return NewLegalHoldStoreFromClient(client), nil
}

// NewLegalHoldStoreFromClient creates a LegalHoldStore on an existing client,
// such as the gateway's shared one. Do not Close it then. It fences the audit
// chain under ChainKeyPrefix; a Chainer with another prefix needs
// WithChainKeyPrefix.
func NewLegalHoldStoreFromClient(client redis.UniversalClient) *LegalHoldStore {
	return &LegalHoldStore{client: client, chainPrefix: ChainKeyPrefix}
}

// WithChainKeyPrefix fences the chain streams of a Chainer built with the
// same keyPrefix. The retention sweeper skips a tenant whose stream the
// store does not fence.
func (s *LegalHoldStore) WithChainKeyPrefix(prefix string) *LegalHoldStore {
	if prefix == "" {
		prefix = ChainKeyPrefix
	}
	s.chainPrefix = prefix
	return s
}

// legalHoldFenceKey is the fence key of a chain stream, in the stream's
// hash slot: the stream key becomes the hash tag, or the key keeps the tag
// the stream already has.
func legalHoldFenceKey(streamKey string) string {
	if i := strings.IndexByte(streamKey, '{'); i >= 0 {
		if j := strings.IndexByte(streamKey[i+1:], '}'); j > 0 {
			return streamKey + legalHoldFenceSuffix
		}
	}
	return "{" + streamKey + "}" + legalHoldFenceSuffix
}

func (s *LegalHoldStore) fenceKey(tenant string) string {
	return legalHoldFenceKey(s.chainPrefix + tenant)
}

func (s *LegalHoldStore) inflightKey(tenant string) string {
	return legalHoldInflightPrefix + tenant
}

// fenceDeleteScript deletes a fence (KEYS[1]) only while it still names the
// hold ARGV[1], so clearing a stale fence never touches a newer one.
var fenceDeleteScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0
`)

// CreateHold creates a new legal hold on a tenant's audit data. When it
// returns the hold, no deletion of that data completes: the hold record is
// written, the tenant's fence stops every trim at the step that would delete,
// and the deletions registered before the fence have ended. If the context
// ends before they have, the hold stays in place and is returned with
// ErrHoldUnconfirmed.
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
		ID:        newHoldID(),
		TenantID:  tenantID,
		Reason:    reason,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}

	data, err := json.Marshal(hold)
	if err != nil {
		return nil, fmt.Errorf("marshal hold: %w", err)
	}

	// The tenant index, the hold record and the fence hash to different
	// Redis Cluster slots, so they are written one at a time, index first:
	// a failure in between leaves an index entry without a record, which
	// every reader skips (no hold), rather than a record ListHolds("") would
	// show as an active hold that IsUnderHold never sees. The fence comes
	// last, so a fence always names a record that exists.
	if err := s.client.SAdd(ctx, legalHoldSetPrefix+tenantID, hold.ID).Err(); err != nil {
		return nil, fmt.Errorf("redis create hold: %w", err)
	}
	if err := s.client.Set(ctx, legalHoldKeyPrefix+hold.ID, data, 0).Err(); err != nil {
		return nil, fmt.Errorf("redis create hold: %w", err)
	}
	if err := s.client.Set(ctx, s.fenceKey(tenantID), hold.ID, 0).Err(); err != nil {
		// Best effort: without its fence the hold is not what CreateHold
		// promises, so the caller gets an error and a clean retry.
		_ = s.client.Del(ctx, legalHoldKeyPrefix+hold.ID).Err()
		_ = s.client.SRem(ctx, legalHoldSetPrefix+tenantID, hold.ID).Err()
		return nil, fmt.Errorf("redis fence hold: %w", err)
	}
	if err := s.awaitInflight(ctx, tenantID); err != nil {
		return hold, err
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
		err := redisutil.ScanEach(ctx, s.client, legalHoldKeyPrefix+"*", 100, func(keys []string) error {
			for _, key := range keys {
				ids = append(ids, strings.TrimPrefix(key, legalHoldKeyPrefix))
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("redis scan holds: %w", err)
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
	// Best effort: a fence left behind names a released hold, and the next
	// hold check clears it (see fenced).
	_ = fenceDeleteScript.Run(ctx, s.client, []string{s.fenceKey(hold.TenantID)}, id).Err()
	return nil
}

// IsUnderHold returns true if the tenant has any active (unreleased) legal
// hold, or a fence naming one. A fence whose hold is released or gone is
// stale and is cleared.
func (s *LegalHoldStore) IsUnderHold(ctx context.Context, tenant string) (bool, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return false, nil
	}
	hold, err := s.activeHoldForTenant(ctx, tenant)
	if err != nil {
		return false, err
	}
	if hold != nil {
		return true, nil
	}
	return s.fenced(ctx, tenant)
}

// fenced reports whether the tenant's fence names an active hold. A fence
// is written after its record (CreateHold), so a fence without an active
// record is stale and is cleared, unless the record cannot be read.
func (s *LegalHoldStore) fenced(ctx context.Context, tenant string) (bool, error) {
	key := s.fenceKey(tenant)
	id, err := s.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis get hold fence: %w", err)
	}
	hold, err := s.GetHold(ctx, id)
	switch {
	case errors.Is(err, ErrHoldNotFound):
	case err != nil:
		return false, err
	case hold.IsActive():
		return true, nil
	}
	if err := fenceDeleteScript.Run(ctx, s.client, []string{key}, id).Err(); err != nil {
		return false, fmt.Errorf("redis clear hold fence: %w", err)
	}
	return false, nil
}

// DeleteUnlessHeld runs del for key, one piece of the tenant's data, unless
// the tenant is under hold. It registers the deletion first, so a CreateHold
// that lands after the hold check waits for del to end before it returns.
// It reports whether del ran; a failed hold check is returned as a
// HoldCheckError and keeps the data.
func (s *LegalHoldStore) DeleteUnlessHeld(ctx context.Context, tenant, key string, del func(context.Context) error) (bool, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return false, errors.New("legal hold: tenant required")
	}
	member := uuid.NewString() + " " + key
	if err := s.registerInflight(ctx, tenant, member); err != nil {
		return false, HoldCheckError{err}
	}
	defer s.unregisterInflight(ctx, tenant, member)
	held, err := s.IsUnderHold(ctx, tenant)
	if err != nil {
		return false, HoldCheckError{err}
	}
	if held {
		return false, nil
	}
	if err := del(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// HoldCheckError is a failed legal hold check: the data is kept.
type HoldCheckError struct{ Err error }

func (e HoldCheckError) Error() string { return "legal hold check: " + e.Err.Error() }
func (e HoldCheckError) Unwrap() error { return e.Err }

// registerInflight records a deletion attempt, scored by the Redis clock,
// which every replica shares.
func (s *LegalHoldStore) registerInflight(ctx context.Context, tenant, member string) error {
	now, err := s.client.Time(ctx).Result()
	if err != nil {
		return fmt.Errorf("redis time: %w", err)
	}
	pipe := s.client.Pipeline()
	pipe.ZAdd(ctx, s.inflightKey(tenant), redis.Z{Score: float64(now.UnixMilli()), Member: member})
	pipe.Expire(ctx, s.inflightKey(tenant), legalHoldInflightTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis register deletion: %w", err)
	}
	return nil
}

// unregisterInflight ends a deletion attempt, even when ctx is done: a
// registration left behind holds up CreateHold until it goes stale.
func (s *LegalHoldStore) unregisterInflight(ctx context.Context, tenant, member string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.client.ZRem(ctx, s.inflightKey(tenant), member).Err()
}

// awaitInflight returns once no deletion of the tenant's data registered
// within legalHoldInflightStale is in flight, or ErrHoldUnconfirmed when
// ctx ends first.
func (s *LegalHoldStore) awaitInflight(ctx context.Context, tenant string) error {
	for {
		now, err := s.client.Time(ctx).Result()
		if err != nil {
			return fmt.Errorf("%w: redis time: %v", ErrHoldUnconfirmed, err)
		}
		since := strconv.FormatInt(now.Add(-legalHoldInflightStale).UnixMilli(), 10)
		n, err := s.client.ZCount(ctx, s.inflightKey(tenant), since, "+inf").Result()
		if err != nil {
			return fmt.Errorf("%w: redis read deletions in flight: %v", ErrHoldUnconfirmed, err)
		}
		if n == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %d still in flight: %v", ErrHoldUnconfirmed, n, ctx.Err())
		case <-time.After(legalHoldInflightPoll):
		}
	}
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
	// ErrHoldUnconfirmed comes with a hold that is in place and blocks every
	// deletion from now on, but whose CreateHold could not wait for the
	// deletions that were in flight when it was created.
	ErrHoldUnconfirmed = fmt.Errorf("legal hold recorded but not confirmed: deletions were still in flight")
)
