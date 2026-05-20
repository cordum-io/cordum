// EDGE-143.6 — Redis backend for the operator-exception API.
// Keyspace per §10.5 with one addition (exception:tenant) so the
// MatchActiveExceptions path doesn't need a Redis SCAN:
//
//	edge:shadow:exception:<exception_id>           — JSON record
//	edge:shadow:exception:tenant:<tenant_id>       — ZSET, members=exception_ids, score=created_at unix-ms
//	edge:shadow:index:exception:<exception_id>     — ZSET, members=finding_ids stamped by this exception
//
// All reads tenant-gate via the tenant_id field on the loaded JSON, so
// a foreign-tenant exception_id resolves to ErrNotFound for the
// requesting tenant (parity with GetFinding's probe defense).
package shadow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const (
	redisKeyException             = "edge:shadow:exception:"
	redisKeyExceptionTenantIndex  = "edge:shadow:exception:tenant:"
	redisIndexKeyExceptionMembers = "edge:shadow:index:exception:"
	// exceptionListDefaultLimit bounds the page size on the operator
	// list endpoint when the caller omits ?limit=. The tenant cap
	// (maxExceptionsPerTenant=1000) is the upper bound; this default
	// keeps responses small enough to render in the dashboard without
	// pagination ceremony for the common case.
	exceptionListDefaultLimit     = 50
	exceptionCreateCASMaxAttempts = 10
	exceptionRevokeCASMaxAttempts = 5
)

type exceptionReadClient interface {
	ZRevRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	MGet(ctx context.Context, keys ...string) *redis.SliceCmd
}

// exceptionKey returns the per-record JSON key.
func exceptionKey(id string) string {
	return redisKeyException + id
}

// exceptionTenantIndexKey returns the per-tenant exception index.
func exceptionTenantIndexKey(tenantID string) string {
	return redisKeyExceptionTenantIndex + tenantID
}

// exceptionMembersIndexKey returns the per-exception finding-membership
// index. Each member is a finding_id that the exception suppressed.
func exceptionMembersIndexKey(exceptionID string) string {
	return redisIndexKeyExceptionMembers + exceptionID
}

// defaultExceptionIDGen mints a 32-hex-char id with the exception
// prefix applied. defaultIDGen returns the raw hex; we wrap it because
// callers may inject WithIDGen for deterministic tests.
func (s *RedisStore) defaultExceptionIDGen() string {
	return exceptionIDPrefix + s.idGen()
}

// CreateException persists a new exception record.
func (s *RedisStore) CreateException(ctx context.Context, req CreateExceptionRequest) (*Exception, error) {
	if s == nil || s.client == nil {
		return nil, ErrStoreUnavailable
	}
	exc, err := normalizeAndValidateException(req, s.now(), s.defaultExceptionIDGen)
	if err != nil {
		return nil, err
	}

	tenantIdxKey := exceptionTenantIndexKey(exc.TenantID)
	payload, err := json.Marshal(exc)
	if err != nil {
		return nil, fmt.Errorf("shadow exception: marshal: %w", err)
	}
	score := float64(exc.CreatedAt.UnixMilli())

	err = redisutil.Retry(ctx, s.client, func(tx *redis.Tx) error {
		// Enforce per-tenant cap inside the watched transaction so
		// concurrent creators cannot all pass the same stale ZCARD.
		count, err := tx.ZCard(ctx, tenantIdxKey).Result()
		if err != nil {
			return fmt.Errorf("shadow exception: zcard: %w", err)
		}
		if count >= int64(maxExceptionsPerTenant) {
			return fmt.Errorf("%w: tenant has %d active exceptions (cap %d)", ErrExceptionLimitExceeded, count, maxExceptionsPerTenant)
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, exceptionKey(exc.ExceptionID), payload, 0)
			pipe.ZAdd(ctx, tenantIdxKey, redis.Z{Score: score, Member: exc.ExceptionID})
			return nil
		})
		if err != nil {
			if !errors.Is(err, redis.TxFailedErr) {
				// Preserve the previous best-effort cleanup behavior for
				// non-CAS pipeline failures after Redis executes commands.
				_ = s.client.Del(ctx, exceptionKey(exc.ExceptionID)).Err()
			}
			return fmt.Errorf("shadow exception: pipeline: %w", err)
		}
		return nil
	}, redisutil.WithKeys(tenantIdxKey), redisutil.WithMaxAttempts(exceptionCreateCASMaxAttempts))
	if errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		return nil, fmt.Errorf("%w: tenant cap update retry exhausted", ErrExceptionLimitExceeded)
	}
	if err != nil {
		return nil, err
	}
	return exc, nil
}

// GetException loads an exception and enforces tenant ownership.
func (s *RedisStore) GetException(ctx context.Context, tenantID, exceptionID string) (*Exception, error) {
	if s == nil || s.client == nil {
		return nil, ErrStoreUnavailable
	}
	tenantID = strings.TrimSpace(tenantID)
	exceptionID = strings.TrimSpace(exceptionID)
	if tenantID == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ErrValidation)
	}
	if exceptionID == "" {
		return nil, fmt.Errorf("%w: exception_id is required", ErrValidation)
	}
	data, err := s.client.Get(ctx, exceptionKey(exceptionID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("shadow exception: get: %w", err)
	}
	var exc Exception
	if err := json.Unmarshal(data, &exc); err != nil {
		return nil, fmt.Errorf("shadow exception: unmarshal: %w", err)
	}
	if exc.TenantID != tenantID {
		// Cross-tenant probe defense — same status code as the missing
		// case so callers can't enumerate tuples across tenants.
		return nil, ErrNotFound
	}
	// Lazy expiry transition — surface the up-to-date status to the
	// caller without blocking on a sweeper.
	if exc.Status == ExceptionStatusActive && !exc.ExpiresAt.After(s.now()) {
		exc.Status = ExceptionStatusExpired
	}
	return &exc, nil
}

// ListExceptions returns a tenant-scoped, optionally-filtered page.
// Scope filters apply in-memory because the per-tenant index is
// bounded by maxExceptionsPerTenant.
func (s *RedisStore) ListExceptions(ctx context.Context, q ListExceptionsQuery) (ExceptionPage, error) {
	if s == nil || s.client == nil {
		return ExceptionPage{}, ErrStoreUnavailable
	}
	tenantID := strings.TrimSpace(q.TenantID)
	if tenantID == "" {
		return ExceptionPage{}, fmt.Errorf("%w: tenant_id is required", ErrValidation)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = exceptionListDefaultLimit
	}
	if limit > maxExceptionsPerTenant {
		limit = maxExceptionsPerTenant
	}

	// Decode the cursor (the last-seen created_at score). Index is
	// score-descending; we use ZREVRANGEBYSCORE.
	maxScore := "+inf"
	if c := strings.TrimSpace(q.Cursor); c != "" {
		if _, err := strconv.ParseFloat(c, 64); err != nil {
			return ExceptionPage{}, ErrInvalidCursor
		}
		// Half-open: skip the cursor entry itself.
		maxScore = "(" + c
	}

	ids, err := s.client.ZRevRangeByScore(ctx, exceptionTenantIndexKey(tenantID), &redis.ZRangeBy{
		Min:    "-inf",
		Max:    maxScore,
		Count:  int64(limit) * int64(overScanFactor),
		Offset: 0,
	}).Result()
	if err != nil {
		return ExceptionPage{}, fmt.Errorf("shadow exception: index range: %w", err)
	}
	if len(ids) == 0 {
		return ExceptionPage{}, nil
	}

	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = exceptionKey(id)
	}
	raws, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return ExceptionPage{}, fmt.Errorf("shadow exception: mget: %w", err)
	}

	now := s.now()
	status := ExceptionStatus(strings.ToLower(strings.TrimSpace(string(q.Status))))
	sourceType := strings.ToLower(strings.TrimSpace(q.ScopeSourceType))
	risk := FindingRisk(strings.ToLower(strings.TrimSpace(string(q.ScopeRiskLevel))))

	out := make([]Exception, 0, limit)
	var lastScore int64
	for i, raw := range raws {
		if i >= int(int64(limit)*int64(overScanFactor)) {
			break
		}
		if raw == nil {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			continue
		}
		var exc Exception
		if err := json.Unmarshal([]byte(s), &exc); err != nil {
			continue
		}
		if exc.TenantID != tenantID {
			continue
		}
		if exc.Status == ExceptionStatusActive && !exc.ExpiresAt.After(now) {
			exc.Status = ExceptionStatusExpired
		}
		if status != "" && exc.Status != status {
			continue
		}
		if sourceType != "" && exc.ScopeSourceType != sourceType {
			continue
		}
		if risk != "" && exc.ScopeRiskLevel != risk {
			continue
		}
		out = append(out, exc)
		lastScore = exc.CreatedAt.UnixMilli()
		if len(out) >= limit {
			break
		}
	}

	page := ExceptionPage{Exceptions: out}
	if len(out) >= limit && len(ids) > limit {
		page.NextCursor = strconv.FormatInt(lastScore, 10)
	}
	return page, nil
}

// RevokeException transitions an active exception to revoked.
func (s *RedisStore) RevokeException(ctx context.Context, tenantID, exceptionID string, req RevokeExceptionRequest) (*Exception, error) {
	if s == nil || s.client == nil {
		return nil, ErrStoreUnavailable
	}
	tenantID = strings.TrimSpace(tenantID)
	exceptionID = strings.TrimSpace(exceptionID)
	revoker := strings.TrimSpace(req.RevokedBy)
	if revoker == "" {
		return nil, fmt.Errorf("%w: revoked_by is required", ErrValidation)
	}

	key := exceptionKey(exceptionID)
	reason := strings.TrimSpace(req.Reason)
	var revoked *Exception
	err := redisutil.Retry(ctx, s.client, func(tx *redis.Tx) error {
		next, err := s.revokeExceptionTx(ctx, tx, key, tenantID, exceptionID, revoker, reason)
		if err != nil {
			return err
		}
		revoked = next
		return nil
	}, redisutil.WithKeys(key), redisutil.WithMaxAttempts(exceptionRevokeCASMaxAttempts))
	if errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		return nil, fmt.Errorf("%w: revoke retry exhausted", ErrTerminalConflict)
	}
	if err != nil {
		return nil, err
	}
	return revoked, nil
}

func (s *RedisStore) revokeExceptionTx(
	ctx context.Context,
	tx *redis.Tx,
	key, tenantID, exceptionID, revoker, reason string,
) (*Exception, error) {
	data, err := tx.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("shadow exception: get: %w", err)
	}
	var exc Exception
	if err := json.Unmarshal(data, &exc); err != nil {
		return nil, fmt.Errorf("shadow exception: unmarshal: %w", err)
	}
	if exc.TenantID != tenantID {
		return nil, ErrNotFound
	}
	if err := prepareExceptionRevoke(&exc, revoker, reason, s.now()); err != nil {
		if errors.Is(err, errExceptionRevokeIdempotent) {
			return &exc, nil
		}
		return nil, err
	}
	payload, err := json.Marshal(&exc)
	if err != nil {
		return nil, fmt.Errorf("shadow exception: marshal: %w", err)
	}
	_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, key, payload, 0)
		pipe.ZRem(ctx, exceptionTenantIndexKey(tenantID), exceptionID)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("shadow exception: revoke pipeline: %w", err)
	}
	return &exc, nil
}

var errExceptionRevokeIdempotent = errors.New("shadow exception: revoke idempotent")

func prepareExceptionRevoke(exc *Exception, revoker, reason string, now time.Time) error {
	switch exc.Status {
	case ExceptionStatusActive:
	case ExceptionStatusRevoked:
		if exc.RevokedBy == revoker {
			return errExceptionRevokeIdempotent
		}
		return fmt.Errorf("%w: already revoked by %s", ErrTerminalConflict, exc.RevokedBy)
	case ExceptionStatusExpired:
		return fmt.Errorf("%w: exception already expired", ErrTerminalConflict)
	}
	exc.Status = ExceptionStatusRevoked
	exc.RevokedBy = revoker
	revokedAt := now
	exc.RevokedAt = &revokedAt
	exc.RevocationReason = reason
	return nil
}

// MatchActiveExceptions scans the tenant's active exception index and
// returns those whose scope predicate matches the supplied finding.
// Bounded by maxExceptionsPerTenant; safe to call inline from
// CreateFinding's emit path.
func (s *RedisStore) MatchActiveExceptions(ctx context.Context, f *ShadowAgentFinding) ([]Exception, error) {
	if s == nil || s.client == nil {
		return nil, ErrStoreUnavailable
	}
	return s.matchActiveExceptions(ctx, s.client, f)
}

func (s *RedisStore) matchActiveExceptions(ctx context.Context, client exceptionReadClient, f *ShadowAgentFinding) ([]Exception, error) {
	if f == nil || strings.TrimSpace(f.TenantID) == "" {
		return nil, nil
	}
	ids, err := client.ZRevRange(ctx, exceptionTenantIndexKey(f.TenantID), 0, int64(maxExceptionsPerTenant)-1).Result()
	if err != nil {
		return nil, fmt.Errorf("shadow exception: tenant index range: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = exceptionKey(id)
	}
	raws, err := client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("shadow exception: mget: %w", err)
	}
	now := s.now()
	out := make([]Exception, 0, 4)
	for _, raw := range raws {
		if raw == nil {
			continue
		}
		str, ok := raw.(string)
		if !ok {
			continue
		}
		var exc Exception
		if err := json.Unmarshal([]byte(str), &exc); err != nil {
			continue
		}
		if exc.matchesFinding(f, now) {
			out = append(out, exc)
		}
	}
	return out, nil
}

// recordExceptionMembership adds the finding_id to the exception's
// membership index. Best-effort: any error is logged-and-ignored
// upstream so finding creation does not fail on index churn.
func (s *RedisStore) recordExceptionMembership(ctx context.Context, exceptionID, findingID string, ts time.Time) error {
	if s == nil || s.client == nil {
		return ErrStoreUnavailable
	}
	if exceptionID == "" || findingID == "" {
		return nil
	}
	return s.client.ZAdd(ctx, exceptionMembersIndexKey(exceptionID), redis.Z{
		Score:  float64(ts.UnixMilli()),
		Member: findingID,
	}).Err()
}
