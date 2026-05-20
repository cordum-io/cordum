package shadow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis keyspace for shadow findings. Pinned per PRD:
//
//	edge:shadow:finding:<finding_id>            — JSON record (string)
//	edge:shadow:index:<tenant_id>                — ZSET, score=created_at unix-ms
//	edge:shadow:index:<tenant_id>:status:<v>     — ZSET, secondary filter
//	edge:shadow:index:<tenant_id>:risk:<v>       — ZSET, secondary filter
//	edge:shadow:index:<tenant_id>:agent:<v>      — ZSET, secondary filter
//	edge:shadow:index:<tenant_id>:owner:<v>      — ZSET, secondary filter
//
// All secondary indexes share the same per-tenant scope to prevent
// cross-tenant index contamination. The tenant index is the broadest
// fallback; secondaries are used when the query's narrowest filter
// matches one of (status, risk, agent_product, owner).
const (
	redisKeyFinding     = "edge:shadow:finding:"
	redisKeyIndexTenant = "edge:shadow:index:"
	redisIndexSegStatus = ":status:"
	redisIndexSegRisk   = ":risk:"
	redisIndexSegAgent  = ":agent:"
	redisIndexSegOwner  = ":owner:"
	// EDGE-143.5 — §10.5 NON-tenant-scoped indexes. Multiple tenants
	// share the same ZSET on these keys (Q7 binding governor ruling,
	// comment-a17f4f1c on task-de50a293). Tenant isolation is enforced
	// at read time via the gate in ListFindings; cross-tenant members
	// are SKIPPED (never deleted) by the indexIsTenantScoped=false
	// branch — see the data-loss guard there.
	redisIndexKeySource  = "edge:shadow:index:source:"
	redisIndexKeyCluster = "edge:shadow:index:cluster:"
	redisIndexKeyRepo    = "edge:shadow:index:repo:"
	redisIndexKeySignal  = "edge:shadow:index:signal:"
	// overScanFactor controls how many index entries we pull per page when
	// secondary filters require post-fetch JSON validation. 3x balances
	// read amplification vs. round-trips on dense filter combinations.
	overScanFactor            = 3
	createFindingMaxTxRetries = 5
	transitionMaxTxRetries    = 5
	staleCleanupMaxTxRetries  = 3
	staleCleanupMaxIDs        = MaxListPageSize * overScanFactor
	staleCleanupMaxConcurrent = 1
	staleCleanupTimeout       = 2 * time.Second
)

// RedisStore is the production Store backed by the shared gateway Redis
// client. It does NOT own the client (NewRedisStore stores the
// caller-owned client by reference) so callers control lifecycle.
type RedisStore struct {
	client                redis.UniversalClient
	now                   func() time.Time
	idGen                 func() string
	terminalRetention     time.Duration
	staleCleanupMu        sync.Mutex
	staleCleanupSlots     chan struct{}
	staleCleanupInFlight  map[string]struct{}
	staleCleanupStartHook func(context.Context, string, []string)
	// shadowRetention maps each §10.5 ShadowFindingRetentionClass to a
	// terminal TTL. Empty class falls back to terminalRetention. Defaults
	// from defaultShadowRetention(); overridable via
	// WithShadowRetentionClasses or the CORDUM_EDGE_SHADOW_RETENTION_*
	// env vars (read once in NewRedisStore).
	shadowRetention map[ShadowFindingRetentionClass]time.Duration
}

// StoreOption customizes RedisStore behavior. Primarily for tests that
// need to pin time, ids, or shorten retention windows.
type StoreOption func(*RedisStore)

// WithClock pins the store clock. Tests use this to make timestamps
// deterministic; production calls NewRedisStore without options.
func WithClock(now func() time.Time) StoreOption {
	return func(s *RedisStore) { s.now = now }
}

// WithIDGen pins the synthetic finding-id generator. Tests use this to
// produce stable ids without dragging crypto/rand into assertions.
func WithIDGen(gen func() string) StoreOption {
	return func(s *RedisStore) { s.idGen = gen }
}

// WithTerminalRetention overrides the TTL applied to resolved/suppressed
// records. Zero disables retention (records persist until manually
// pruned); useful for compliance-tenant configurations where shadow
// dispositions must be retained indefinitely.
func WithTerminalRetention(d time.Duration) StoreOption {
	return func(s *RedisStore) { s.terminalRetention = d }
}

// WithShadowRetentionClasses overrides the per-§10.5 retention-class
// TTL map. Nil resets to defaults. Empty values mean "fall back to
// terminalRetention" — useful for compliance configurations where
// shadow records are retained indefinitely.
func WithShadowRetentionClasses(m map[ShadowFindingRetentionClass]time.Duration) StoreOption {
	return func(s *RedisStore) {
		if m == nil {
			s.shadowRetention = defaultShadowRetention()
			return
		}
		copied := make(map[ShadowFindingRetentionClass]time.Duration, len(m))
		for k, v := range m {
			copied[k] = v
		}
		s.shadowRetention = copied
	}
}

// defaultShadowRetention returns the §10.5 baseline TTL map:
// shadow_short=7d, shadow_default=90d, shadow_long=365d.
func defaultShadowRetention() map[ShadowFindingRetentionClass]time.Duration {
	return map[ShadowFindingRetentionClass]time.Duration{
		ShadowRetentionShort:   7 * 24 * time.Hour,
		ShadowRetentionDefault: 90 * 24 * time.Hour,
		ShadowRetentionLong:    365 * 24 * time.Hour,
	}
}

// NewRedisStore wraps the shared redis client. Returns (nil, nil) when
// client is nil so callers can pass through a missing-store sentinel
// without allocating a non-functional store. Returns an error when one
// of the CORDUM_EDGE_SHADOW_RETENTION_* env vars fails to parse or is
// non-positive, per §10.5 "positive durations; 0/negative fail at
// startup".
func NewRedisStore(client redis.UniversalClient, opts ...StoreOption) (*RedisStore, error) {
	if client == nil {
		return nil, nil
	}
	envRetention, err := shadowRetentionFromEnv(defaultShadowRetention())
	if err != nil {
		return nil, err
	}
	s := &RedisStore{
		client:            client,
		now:               func() time.Time { return time.Now().UTC() },
		idGen:             defaultIDGen,
		terminalRetention: DefaultTerminalRetention,
		shadowRetention:   envRetention,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	if s.idGen == nil {
		s.idGen = defaultIDGen
	}
	if s.shadowRetention == nil {
		s.shadowRetention = defaultShadowRetention()
	}
	return s, nil
}

// shadowRetentionFromEnv overlays env-var values onto the defaults.
// Env vars: CORDUM_EDGE_SHADOW_RETENTION_SHORT, _DEFAULT, _LONG. Empty
// → use default; malformed or non-positive → error.
func shadowRetentionFromEnv(base map[ShadowFindingRetentionClass]time.Duration) (map[ShadowFindingRetentionClass]time.Duration, error) {
	out := make(map[ShadowFindingRetentionClass]time.Duration, len(base))
	for k, v := range base {
		out[k] = v
	}
	envs := []struct {
		envKey string
		rc     ShadowFindingRetentionClass
	}{
		{"CORDUM_EDGE_SHADOW_RETENTION_SHORT", ShadowRetentionShort},
		{"CORDUM_EDGE_SHADOW_RETENTION_DEFAULT", ShadowRetentionDefault},
		{"CORDUM_EDGE_SHADOW_RETENTION_LONG", ShadowRetentionLong},
	}
	for _, e := range envs {
		raw := strings.TrimSpace(os.Getenv(e.envKey))
		if raw == "" {
			continue
		}
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("shadow finding: env %s=%q: %w", e.envKey, raw, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("shadow finding: env %s=%q must be a positive duration", e.envKey, raw)
		}
		out[e.rc] = d
	}
	return out, nil
}

func defaultIDGen() string {
	// 16 bytes of entropy → 32 hex chars; the findingIDPrefix is
	// applied by the normaliser. crypto/rand to avoid collisions across
	// concurrent emit paths.
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("ts%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

// findingKey returns the per-record JSON key.
func findingKey(id string) string {
	return redisKeyFinding + id
}

// tenantIndexKey returns the broad tenant-scoped index key.
func tenantIndexKey(tenant string) string {
	return redisKeyIndexTenant + tenant
}

// secondaryIndexKey returns a tenant-scoped index key for the given
// filter dimension (status/risk/agent/owner). seg is one of the
// redisIndexSeg* constants.
func secondaryIndexKey(tenant, seg, value string) string {
	return redisKeyIndexTenant + tenant + seg + value
}

// sourceIndexKey / clusterIndexKey / repoIndexKey / signalIndexKey
// return the §10.5 shared (cross-tenant) index keys. See the constant
// block above for the cross-tenant safety contract.
func sourceIndexKey(sourceType string) string {
	return redisIndexKeySource + sourceType
}

func clusterIndexKey(clusterID string) string {
	return redisIndexKeyCluster + clusterID
}

func repoIndexKey(provider, repo string) string {
	return redisIndexKeyRepo + provider + ":" + repo
}

func signalIndexKey(signal string) string {
	return redisIndexKeySignal + signal
}

// CreateFinding persists a new finding. Atomicity: the JSON write +
// every index member add happen inside a Redis pipeline so a single
// partial failure leaves no orphaned indexes.
func (s *RedisStore) CreateFinding(ctx context.Context, req CreateFindingRequest) (*ShadowAgentFinding, error) {
	if s == nil || s.client == nil {
		return nil, ErrStoreUnavailable
	}
	finding, err := normalizeAndValidateCreate(req, s.now(), s.idGen)
	if err != nil {
		return nil, err
	}

	// EDGE-143.6 — emit-time exception suppression. If the caller did
	// NOT pre-stamp an exception_id (the operator-resolve path may
	// pre-stamp), scan the tenant's active exception index for a scope
	// match. A match flips the finding to managed_skip with
	// false_positive_reason="operator_exception" before persistence so
	// the suppressed record never appears in the default detected
	// stream. The shadow_agent.exception_applied audit event is emitted
	// by the gateway handler after CreateFinding returns (the store
	// does not own the audit exporter).
	key := findingKey(finding.FindingID)
	created, inserted, err := s.createFindingAtomically(ctx, key, finding)
	if err != nil {
		return nil, err
	}
	// EDGE-143.6 — record membership in the exception's per-exception
	// finding index. Best-effort; index churn must not fail the create.
	if inserted && created.ExceptionID != "" {
		_ = s.recordExceptionMembership(ctx, created.ExceptionID, created.FindingID, created.CreatedAt)
	}
	return created, nil
}

func (s *RedisStore) createFindingAtomically(
	ctx context.Context,
	key string,
	finding *ShadowAgentFinding,
) (*ShadowAgentFinding, bool, error) {
	var created *ShadowAgentFinding
	var inserted bool
	watchKeys := []string{key}
	if finding.ExceptionID == "" {
		watchKeys = append(watchKeys, exceptionTenantIndexKey(finding.TenantID))
	}
	for attempt := 0; attempt < createFindingMaxTxRetries; attempt++ {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			candidate := *finding
			if candidate.ExceptionID == "" {
				if err := s.applyActiveExceptionMatch(ctx, tx, &candidate); err != nil {
					return err
				}
			}
			payload, err := json.Marshal(&candidate)
			if err != nil {
				return fmt.Errorf("shadow finding: marshal: %w", err)
			}
			existing, getErr := tx.Get(ctx, key).Bytes()
			if getErr == nil {
				prev, err := decodeExistingFindingForCreate(existing)
				if err != nil {
					return err
				}
				if prev.TenantID == candidate.TenantID && bytes.Equal(existing, payload) {
					created = prev
					return nil
				}
				return fmt.Errorf("%w: finding_id %s", ErrAlreadyExists, candidate.FindingID)
			}
			if !errors.Is(getErr, redis.Nil) {
				return fmt.Errorf("shadow finding: probe: %w", getErr)
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, payload, 0)
				addFindingIndexWrites(ctx, pipe, &candidate, float64(candidate.CreatedAt.UnixMilli()))
				return nil
			})
			if err != nil {
				return fmt.Errorf("shadow finding: create transaction: %w", err)
			}
			created = &candidate
			inserted = true
			return nil
		}, watchKeys...)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if created != nil {
			return created, inserted, nil
		}
	}
	return nil, false, fmt.Errorf("shadow finding: create transaction retries exhausted: %w", redis.TxFailedErr)
}

func (s *RedisStore) applyActiveExceptionMatch(ctx context.Context, tx exceptionReadClient, finding *ShadowAgentFinding) error {
	matches, err := s.matchActiveExceptions(ctx, tx, finding)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return nil
	}
	applied := matches[0]
	finding.ExceptionID = applied.ExceptionID
	finding.FalsePositiveReason = FalsePositiveReasonOperatorException
	finding.Status = FindingStatusManagedSkip
	return nil
}

func decodeExistingFindingForCreate(raw []byte) (*ShadowAgentFinding, error) {
	var prev ShadowAgentFinding
	if err := json.Unmarshal(raw, &prev); err != nil {
		return nil, fmt.Errorf("shadow finding: unmarshal existing: %w", err)
	}
	applyReadDefaults(&prev)
	return &prev, nil
}

func addFindingIndexWrites(ctx context.Context, pipe redis.Pipeliner, finding *ShadowAgentFinding, score float64) {
	pipe.ZAdd(ctx, tenantIndexKey(finding.TenantID), redis.Z{Score: score, Member: finding.FindingID})
	pipe.ZAdd(ctx, secondaryIndexKey(finding.TenantID, redisIndexSegStatus, string(finding.Status)),
		redis.Z{Score: score, Member: finding.FindingID})
	pipe.ZAdd(ctx, secondaryIndexKey(finding.TenantID, redisIndexSegRisk, string(finding.Risk)),
		redis.Z{Score: score, Member: finding.FindingID})
	pipe.ZAdd(ctx, secondaryIndexKey(finding.TenantID, redisIndexSegAgent, finding.AgentProduct),
		redis.Z{Score: score, Member: finding.FindingID})
	pipe.ZAdd(ctx, secondaryIndexKey(finding.TenantID, redisIndexSegOwner, finding.OwnerPrincipalID),
		redis.Z{Score: score, Member: finding.FindingID})
	// EDGE-143.5 — §10.5 shared (non-tenant-scoped) indexes.
	pipe.ZAdd(ctx, sourceIndexKey(finding.SourceType), redis.Z{Score: score, Member: finding.FindingID})
	if finding.ClusterID != "" {
		pipe.ZAdd(ctx, clusterIndexKey(finding.ClusterID), redis.Z{Score: score, Member: finding.FindingID})
	}
	if finding.CIProvider != "" && finding.Repo != "" {
		pipe.ZAdd(ctx, repoIndexKey(finding.CIProvider, finding.Repo), redis.Z{Score: score, Member: finding.FindingID})
	}
	for _, sig := range finding.SignalSet {
		if sig != "" {
			pipe.ZAdd(ctx, signalIndexKey(sig), redis.Z{Score: score, Member: finding.FindingID})
		}
	}
}

// GetFinding loads a finding and enforces tenant ownership. Cross-tenant
// access returns ErrNotFound (not a tenant-mismatch error) so callers
// cannot use the get API to probe other tenants' records.
func (s *RedisStore) GetFinding(ctx context.Context, tenantID, findingID string) (*ShadowAgentFinding, error) {
	if s == nil || s.client == nil {
		return nil, ErrStoreUnavailable
	}
	tenantID = strings.TrimSpace(tenantID)
	findingID = strings.TrimSpace(findingID)
	if tenantID == "" || findingID == "" {
		return nil, ErrNotFound
	}
	raw, err := s.client.Get(ctx, findingKey(findingID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("shadow finding: get: %w", err)
	}
	var f ShadowAgentFinding
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("shadow finding: unmarshal: %w", err)
	}
	applyReadDefaults(&f)
	if f.TenantID != tenantID {
		return nil, ErrNotFound
	}
	if s.isExpiredTerminal(&f) {
		// Hide expired-terminal records; the cleanup pass below removes them.
		s.purgeExpired(ctx, &f)
		return nil, ErrNotFound
	}
	return &f, nil
}

// clampListPageSize returns a page size bounded to [1, MaxListPageSize],
// substituting DefaultListPageSize when n is non-positive. Use at every
// make() / loop site that depends on a caller-supplied page limit so
// the bound is visible inside the allocating function's scope. This is
// the named sanitizer CodeQL's go/allocation-size-overflow rule
// recognises; callers MUST go through it before passing the value to
// any allocation primitive (see ListFindings + listFindingsByMultiSignal).
func clampListPageSize(n int) int {
	if n <= 0 {
		return DefaultListPageSize
	}
	if n > MaxListPageSize {
		return MaxListPageSize
	}
	return n
}

// ListFindings selects the narrowest applicable index, then post-filters
// records that don't match the remaining query dimensions. Pagination
// uses an opaque cursor (encoded "<score>:<finding_id>") so callers
// can resume without leaking Redis internals.
func (s *RedisStore) ListFindings(ctx context.Context, q ListFindingsQuery) (FindingPage, error) {
	if s == nil || s.client == nil {
		return FindingPage{}, ErrStoreUnavailable
	}
	tenant := strings.TrimSpace(q.TenantID)
	if tenant == "" {
		return FindingPage{}, fmt.Errorf("%w: tenant_id is required", ErrValidation)
	}
	limit := clampListPageSize(q.Limit)

	// EDGE-143.5 — multi-signal any-of bypasses chooseIndex: scan each
	// signal's shared index, dedupe by finding_id, then apply
	// post-filters. Single-signal is handled by chooseIndex. Bounded by
	// len(signals)*limit*overScanFactor; worst case 16*200*3 = 9600
	// ZSET reads + GETs per page (acceptable for observe-mode reads).
	// Optimize to SUNIONSTORE only if profiling shows hotspot; do not
	// pre-optimize.
	normSignals := normalizeSignals(q.Signals)
	if len(normSignals) > 1 {
		return s.listFindingsByMultiSignal(ctx, tenant, q, normSignals, limit)
	}

	indexKey, postFilters, indexIsTenantScoped := chooseIndex(tenant, q)

	startScore, startID, err := decodeCursor(q.Cursor)
	if err != nil {
		return FindingPage{}, err
	}

	pulled, err := s.zScanDescending(ctx, indexKey, startScore, startID, limit*overScanFactor)
	if err != nil {
		return FindingPage{}, fmt.Errorf("shadow finding: list: %w", err)
	}

	findings := make([]ShadowAgentFinding, 0, limit)
	var staleIDs []string
	var lastMember zMember
	for _, m := range pulled {
		if int64(len(findings)) >= int64(limit) {
			break
		}
		raw, err := s.client.Get(ctx, findingKey(m.member)).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				// Stale index entry; queue for opportunistic cleanup.
				staleIDs = append(staleIDs, m.member)
				continue
			}
			return FindingPage{}, fmt.Errorf("shadow finding: list-get: %w", err)
		}
		var f ShadowAgentFinding
		if err := json.Unmarshal(raw, &f); err != nil {
			staleIDs = append(staleIDs, m.member)
			continue
		}
		applyReadDefaults(&f)
		if f.TenantID != tenant {
			// EDGE-143.5 — when the index we used is NOT tenant-scoped
			// (the new §10.5 source/cluster/repo/signal indexes), cross-
			// tenant index members are EXPECTED and must be skipped
			// without staleIDs cleanup (which would DELETE the other
			// tenant's record — data loss). For tenant-scoped indexes,
			// keep the original defence-in-depth: treat as stale.
			if !indexIsTenantScoped {
				continue
			}
			staleIDs = append(staleIDs, m.member)
			continue
		}
		if s.isExpiredTerminal(&f) {
			staleIDs = append(staleIDs, m.member)
			continue
		}
		if !matchesPostFilters(&f, postFilters) {
			continue
		}
		findings = append(findings, f)
		lastMember = m
	}

	if len(staleIDs) > 0 {
		s.scheduleStaleCleanup(ctx, tenant, staleIDs)
	}

	var nextCursor string
	if int64(len(findings)) >= int64(limit) && lastMember.member != "" {
		nextCursor = encodeCursor(lastMember.score, lastMember.member)
	}

	return FindingPage{Findings: findings, NextCursor: nextCursor}, nil
}

// listFindingsByMultiSignal handles the §10.2 multi-value Signals
// any-of filter. Iterates each signal's shared index, dedupes
// finding_ids, then post-filters. Pagination via cursor is not
// supported in the multi-signal path (single-page only); callers
// requesting >limit findings receive a truncated page without a cursor.
// This matches the plan's "start simple" guidance.
func (s *RedisStore) listFindingsByMultiSignal(
	ctx context.Context,
	tenant string,
	q ListFindingsQuery,
	normSignals []string,
	limit int,
) (FindingPage, error) {
	// Defence-in-depth: handler-layer parseShadowFindingListQuery already
	// caps the signal set at maxShadowSignalSetEntries, but re-checking
	// here ensures a future internal caller that bypasses the handler
	// (e.g. a job runner or sibling store wrapper) cannot trigger
	// unbounded fan-out across signal indexes.
	if len(normSignals) > maxShadowSignalSetEntries {
		return FindingPage{}, fmt.Errorf("%w: signals exceeds max %d entries", ErrValidation, maxShadowSignalSetEntries)
	}
	// Defence-in-depth: ListFindings already clamps via
	// clampListPageSize, but re-clamping in-scope surfaces the bound
	// at the make() sites below for static-analysis dataflow
	// (CodeQL go/allocation-size-overflow) and prevents future
	// callers from skipping the bound.
	limit = clampListPageSize(limit)
	// Build post-filter set EXCLUDING the signals dimension (the
	// per-signal scan already restricts to findings carrying at least
	// one of the requested signals).
	_, postFilters, _ := chooseIndex(tenant, ListFindingsQuery{
		TenantID:           q.TenantID,
		Status:             q.Status,
		Risk:               q.Risk,
		AgentProduct:       q.AgentProduct,
		OwnerPrincipalID:   q.OwnerPrincipalID,
		SourceType:         q.SourceType,
		ClusterID:          q.ClusterID,
		Namespace:          q.Namespace,
		CIProvider:         q.CIProvider,
		Repo:               q.Repo,
		ConfidenceMin:      q.ConfidenceMin,
		FirstSeenAfter:     q.FirstSeenAfter,
		LastSeenBefore:     q.LastSeenBefore,
		ExceptionID:        q.ExceptionID,
		IncludeManagedSkip: q.IncludeManagedSkip,
	})
	// Force the post-filter to NOT re-check signals (the union scan
	// already did).
	postFilters.signals = nil

	seen := make(map[string]struct{}, limit)
	findings := make([]ShadowAgentFinding, 0, limit)
	var staleIDs []string
	perScanLimit := limit * overScanFactor
	for _, sig := range normSignals {
		if int64(len(findings)) >= int64(limit) {
			break
		}
		members, err := s.zScanDescending(ctx, signalIndexKey(sig), 0, "", perScanLimit)
		if err != nil {
			return FindingPage{}, fmt.Errorf("shadow finding: list-signal %q: %w", sig, err)
		}
		for _, m := range members {
			if int64(len(findings)) >= int64(limit) {
				break
			}
			if _, dup := seen[m.member]; dup {
				continue
			}
			seen[m.member] = struct{}{}
			raw, err := s.client.Get(ctx, findingKey(m.member)).Bytes()
			if err != nil {
				if errors.Is(err, redis.Nil) {
					// Signal indexes are shared across tenants; we don't
					// know which tenant owned the deleted finding, so we
					// skip cleanup here. The owner's next list call
					// against a tenant-scoped index will clean it up.
					continue
				}
				return FindingPage{}, fmt.Errorf("shadow finding: list-get: %w", err)
			}
			var f ShadowAgentFinding
			if err := json.Unmarshal(raw, &f); err != nil {
				continue
			}
			applyReadDefaults(&f)
			if f.TenantID != tenant {
				// Cross-tenant: skip silently (shared index).
				continue
			}
			if s.isExpiredTerminal(&f) {
				staleIDs = append(staleIDs, m.member)
				continue
			}
			if !matchesPostFilters(&f, postFilters) {
				continue
			}
			findings = append(findings, f)
		}
	}
	if len(staleIDs) > 0 {
		s.scheduleStaleCleanup(ctx, tenant, staleIDs)
	}
	// Multi-signal path doesn't support cursor pagination (yet). Return
	// the page without NextCursor; callers needing pagination must
	// query per single signal.
	return FindingPage{Findings: findings}, nil
}

// ResolveFinding applies the resolve lifecycle transition atomically:
// the watched JSON read, write, and status-index move happen inside the
// Redis WATCH + MULTI/EXEC transition helper.
func (s *RedisStore) ResolveFinding(ctx context.Context, tenantID, findingID string, req ResolveRequest) (*ShadowAgentFinding, error) {
	return s.transitionFinding(ctx, tenantID, findingID, func(f *ShadowAgentFinding, now time.Time) error {
		return applyResolve(f, req, now)
	})
}

// SuppressFinding applies the suppress lifecycle transition atomically.
func (s *RedisStore) SuppressFinding(ctx context.Context, tenantID, findingID string, req SuppressRequest) (*ShadowAgentFinding, error) {
	return s.transitionFinding(ctx, tenantID, findingID, func(f *ShadowAgentFinding, now time.Time) error {
		return applySuppress(f, req, now)
	})
}

// transitionFinding centralises the WATCH/CAS + index-move dance shared
// by resolve/suppress. The mutate fn is the only per-transition difference.
func (s *RedisStore) transitionFinding(
	ctx context.Context,
	tenantID, findingID string,
	mutate func(*ShadowAgentFinding, time.Time) error,
) (*ShadowAgentFinding, error) {
	if s == nil || s.client == nil {
		return nil, ErrStoreUnavailable
	}
	tenantID = strings.TrimSpace(tenantID)
	findingID = strings.TrimSpace(findingID)
	if tenantID == "" || findingID == "" {
		return nil, ErrNotFound
	}
	key := findingKey(findingID)
	var updated *ShadowAgentFinding
	for attempt := 0; attempt < transitionMaxTxRetries; attempt++ {
		updated = nil
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			next, err := s.transitionFindingWatched(ctx, tx, key, tenantID, findingID, mutate)
			if err != nil {
				return err
			}
			updated = next
			return nil
		}, key)
		if err == nil {
			return updated, nil
		}
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("%w: concurrent transition retry exhausted for %s", ErrTerminalConflict, findingID)
}

func (s *RedisStore) transitionFindingWatched(
	ctx context.Context,
	tx *redis.Tx,
	key, tenantID, findingID string,
	mutate func(*ShadowAgentFinding, time.Time) error,
) (*ShadowAgentFinding, error) {
	raw, err := tx.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("shadow finding: transition-get: %w", err)
	}
	var f ShadowAgentFinding
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("shadow finding: unmarshal: %w", err)
	}
	applyReadDefaults(&f)
	if f.TenantID != tenantID {
		return nil, ErrNotFound
	}
	prevStatus := f.Status
	if err := mutate(&f, s.now()); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(&f)
	if err != nil {
		return nil, fmt.Errorf("shadow finding: marshal: %w", err)
	}
	if err := s.commitTransitionFinding(ctx, tx, key, findingID, tenantID, prevStatus, &f, payload); err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *RedisStore) commitTransitionFinding(
	ctx context.Context,
	tx *redis.Tx,
	key, findingID, tenantID string,
	prevStatus FindingStatus,
	f *ShadowAgentFinding,
	payload []byte,
) error {
	score := float64(f.CreatedAt.UnixMilli())
	_, err := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, key, payload, 0)
		if prevStatus != f.Status {
			pipe.ZRem(ctx, secondaryIndexKey(tenantID, redisIndexSegStatus, string(prevStatus)), findingID)
			pipe.ZAdd(ctx, secondaryIndexKey(tenantID, redisIndexSegStatus, string(f.Status)),
				redis.Z{Score: score, Member: findingID})
		}
		if isTerminal(f.Status) {
			if ttl := s.retentionFor(f.RetentionClass); ttl > 0 {
				pipe.Expire(ctx, key, ttl)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("shadow finding: transition: %w", err)
	}
	return nil
}

func isTerminal(s FindingStatus) bool {
	return s == FindingStatusResolved || s == FindingStatusSuppressed
}

// retentionFor returns the terminal TTL applied to a finding given its
// §10.5 RetentionClass. Empty RetentionClass (legacy EDGE-141 records,
// or callers that didn't set it) falls back to s.terminalRetention so
// pre-EDGE-143.5 behavior is preserved.
func (s *RedisStore) retentionFor(rc ShadowFindingRetentionClass) time.Duration {
	if rc == "" {
		return s.terminalRetention
	}
	if d, ok := s.shadowRetention[rc]; ok && d > 0 {
		return d
	}
	return s.terminalRetention
}

func (s *RedisStore) isExpiredTerminal(f *ShadowAgentFinding) bool {
	if !isTerminal(f.Status) || f.ResolvedAt == nil {
		return false
	}
	ttl := s.retentionFor(f.RetentionClass)
	if ttl <= 0 {
		return false
	}
	return s.now().Sub(*f.ResolvedAt) > ttl
}

// purgeExpired removes a single expired terminal record + its index
// members. Best-effort; errors are swallowed because cleanup is
// secondary to user-facing list/get correctness.
func (s *RedisStore) purgeExpired(ctx context.Context, f *ShadowAgentFinding) {
	if s == nil || s.client == nil || f == nil {
		return
	}
	tenant := f.TenantID
	id := f.FindingID
	s.scheduleStaleCleanup(ctx, tenant, []string{id})
}

func (s *RedisStore) scheduleStaleCleanup(ctx context.Context, tenant string, ids []string) {
	if s == nil || s.client == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupIDs := staleCleanupIDs(ids)
	if strings.TrimSpace(tenant) == "" || len(cleanupIDs) == 0 {
		return
	}
	cleanupIDs = s.markStaleCleanupInFlight(cleanupIDs)
	if len(cleanupIDs) == 0 {
		return
	}
	if !s.acquireStaleCleanupSlot() {
		s.clearStaleCleanupInFlight(cleanupIDs)
		return
	}
	go s.runScheduledStaleCleanup(ctx, tenant, cleanupIDs)
}

func staleCleanupIDs(ids []string) []string {
	size := len(ids)
	if size > staleCleanupMaxIDs {
		size = staleCleanupMaxIDs
	}
	seen := make(map[string]struct{}, size)
	out := make([]string, 0, size)
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) >= staleCleanupMaxIDs {
			break
		}
	}
	return out
}

func (s *RedisStore) markStaleCleanupInFlight(ids []string) []string {
	s.staleCleanupMu.Lock()
	defer s.staleCleanupMu.Unlock()
	if s.staleCleanupInFlight == nil {
		s.staleCleanupInFlight = make(map[string]struct{}, len(ids))
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := s.staleCleanupInFlight[id]; ok {
			continue
		}
		s.staleCleanupInFlight[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *RedisStore) acquireStaleCleanupSlot() bool {
	s.staleCleanupMu.Lock()
	if s.staleCleanupSlots == nil {
		s.staleCleanupSlots = make(chan struct{}, staleCleanupMaxConcurrent)
	}
	slots := s.staleCleanupSlots
	s.staleCleanupMu.Unlock()
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *RedisStore) runScheduledStaleCleanup(ctx context.Context, tenant string, ids []string) {
	defer s.releaseStaleCleanupSlot()
	defer s.clearStaleCleanupInFlight(ids)
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), staleCleanupTimeout)
	defer cancel()
	s.opportunisticCleanup(cleanupCtx, tenant, ids)
}

func (s *RedisStore) releaseStaleCleanupSlot() {
	s.staleCleanupMu.Lock()
	slots := s.staleCleanupSlots
	s.staleCleanupMu.Unlock()
	if slots == nil {
		return
	}
	select {
	case <-slots:
	default:
	}
}

func (s *RedisStore) clearStaleCleanupInFlight(ids []string) {
	s.staleCleanupMu.Lock()
	defer s.staleCleanupMu.Unlock()
	for _, id := range ids {
		delete(s.staleCleanupInFlight, id)
	}
}

func (s *RedisStore) opportunisticCleanup(ctx context.Context, tenant string, ids []string) {
	if s == nil || s.client == nil || len(ids) == 0 {
		return
	}
	if hook := s.getStaleCleanupStartHook(); hook != nil {
		hook(ctx, tenant, append([]string(nil), ids...))
	}
	for _, id := range ids {
		s.cleanupStaleID(ctx, tenant, id)
	}
}

func (s *RedisStore) getStaleCleanupStartHook() func(context.Context, string, []string) {
	s.staleCleanupMu.Lock()
	defer s.staleCleanupMu.Unlock()
	return s.staleCleanupStartHook
}

func (s *RedisStore) cleanupStaleID(ctx context.Context, tenant, id string) {
	key := findingKey(id)
	for attempt := 0; attempt < staleCleanupMaxTxRetries; attempt++ {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			return s.cleanupStaleIDTx(ctx, tx, tenant, id, key)
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return
	}
}

func (s *RedisStore) cleanupStaleIDTx(ctx context.Context, tx *redis.Tx, tenant, id, key string) error {
	raw, err := tx.Get(ctx, key).Bytes()
	if err == nil {
		var current ShadowAgentFinding
		if json.Unmarshal(raw, &current) == nil {
			applyReadDefaults(&current)
			if current.TenantID != "" && current.TenantID != tenant {
				return txTenantScopedCleanup(ctx, tx, tenant, id)
			}
			if current.TenantID == tenant && !s.isExpiredTerminal(&current) {
				return nil
			}
		}
	} else if !errors.Is(err, redis.Nil) {
		return nil
	}
	_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, key)
		addTenantScopedFindingCleanup(ctx, pipe, tenant, id)
		addSharedFindingCleanup(ctx, pipe, id)
		return nil
	})
	return err
}

func txTenantScopedCleanup(ctx context.Context, tx *redis.Tx, tenant, id string) error {
	_, err := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		addTenantScopedFindingCleanup(ctx, pipe, tenant, id)
		return nil
	})
	return err
}

func addTenantScopedFindingCleanup(ctx context.Context, pipe redis.Pipeliner, tenant, id string) {
	pipe.ZRem(ctx, tenantIndexKey(tenant), id)
	for _, seg := range []string{redisIndexSegStatus, redisIndexSegRisk, redisIndexSegAgent, redisIndexSegOwner} {
		// We don't know which secondary-index buckets the id lives in
		// without the original fields. ZREM is idempotent and O(log N),
		// so blasting every known closed-set value is acceptable.
		for _, v := range knownEnumValuesForSeg(seg) {
			pipe.ZRem(ctx, secondaryIndexKey(tenant, seg, v), id)
		}
	}
}

func addSharedFindingCleanup(ctx context.Context, pipe redis.Pipeliner, id string) {
	// EDGE-143.5 — source index is closed-enum, so we blast all values.
	// cluster/repo/signal indexes are open-set and intentionally leak stale
	// members; list reads GET the JSON and skip missing records.
	for _, src := range []string{SourceTypeLocal, SourceTypeKubernetes, SourceTypeCI, SourceTypeNetwork} {
		pipe.ZRem(ctx, sourceIndexKey(src), id)
	}
}

// knownEnumValuesForSeg returns the closed-set values we ZREM during
// stale-index cleanup. For agent/owner segments the value-space is
// open — we accept that cleanup leaks a few stale member entries until
// the next ZADD overwrites them on a re-create.
func knownEnumValuesForSeg(seg string) []string {
	switch seg {
	case redisIndexSegStatus:
		return []string{string(FindingStatusDetected), string(FindingStatusResolved), string(FindingStatusSuppressed)}
	case redisIndexSegRisk:
		return []string{string(FindingRiskLow), string(FindingRiskMedium), string(FindingRiskHigh), string(FindingRiskCritical)}
	default:
		return nil
	}
}

// chooseIndex picks the narrowest index for the query and returns the
// leftover filters that have to be applied post-fetch. The third return
// indicates whether the chosen index is tenant-scoped (true for the
// EDGE-141 indexes) or shared across tenants (false for the EDGE-143.5
// §10.5 cluster/source/repo/signal indexes — see Q7 binding governor
// ruling, comment-a17f4f1c on task-de50a293). Cross-tenant index
// members on shared indexes are filtered out at read time (see
// ListFindings) but MUST NOT be deleted as "stale" — that would be
// cross-tenant data loss.
//
// Priority (most→least selective): repo > cluster > source > signal-single
// > status > risk > agent > owner > tenant. Multi-signal any-of bypasses
// chooseIndex entirely (see ListFindings → multi-signal path). Signal
// is placed AFTER the source/cluster/repo composites because those are
// usually narrower in practice (one repo has many findings, but a single
// signal can fire across thousands of findings) — diverges from the
// plan's "signal > exception_id > repo > cluster > source" only on the
// signal-vs-{repo,cluster,source} order, which is a perf choice with
// no correctness impact (post-filters handle the remaining dimensions).
func chooseIndex(tenant string, q ListFindingsQuery) (string, postFilterSet, bool) {
	post := postFilterSet{
		status:             q.Status,
		risk:               q.Risk,
		agentProduct:       strings.ToLower(strings.TrimSpace(q.AgentProduct)),
		owner:              strings.TrimSpace(q.OwnerPrincipalID),
		sourceType:         strings.ToLower(strings.TrimSpace(q.SourceType)),
		clusterID:          strings.TrimSpace(q.ClusterID),
		namespace:          strings.TrimSpace(q.Namespace),
		ciProvider:         strings.ToLower(strings.TrimSpace(q.CIProvider)),
		repo:               strings.TrimSpace(q.Repo),
		signals:            normalizeSignals(q.Signals),
		confidenceMin:      q.ConfidenceMin,
		firstSeenAfter:     q.FirstSeenAfter,
		lastSeenBefore:     q.LastSeenBefore,
		exceptionID:        strings.TrimSpace(q.ExceptionID),
		includeManagedSkip: q.IncludeManagedSkip,
	}

	// Composite repo index is the narrowest §10.5 index when both
	// ci_provider+repo are provided.
	if post.ciProvider != "" && post.repo != "" {
		idx := repoIndexKey(post.ciProvider, post.repo)
		post.ciProvider = ""
		post.repo = ""
		return idx, post, false
	}
	// Cluster index (Q7-critical).
	if post.clusterID != "" {
		idx := clusterIndexKey(post.clusterID)
		post.clusterID = ""
		return idx, post, false
	}
	// Source-type index — EXCEPT "local". Legacy EDGE-141 findings have
	// no source-index ZADD; using the source index would miss them.
	// §10.4 backward-compat path: fall through to broad-tenant + post-
	// filter (applyReadDefaults maps SourceType="" → "local" on read so
	// the post-filter "local" comparison surfaces legacy rows).
	if post.sourceType != "" && post.sourceType != SourceTypeLocal {
		idx := sourceIndexKey(post.sourceType)
		post.sourceType = ""
		return idx, post, false
	}
	// Single-signal: use the signal index. Multi-signal any-of is
	// handled by ListFindings; we leave signals[] populated as a marker
	// so the caller knows to route via the multi-signal path.
	if len(post.signals) == 1 {
		idx := signalIndexKey(post.signals[0])
		post.signals = nil
		return idx, post, false
	}

	// Existing tenant-scoped selections (unchanged for the EDGE-141
	// dimensions).
	switch {
	case post.status != "":
		idx := secondaryIndexKey(tenant, redisIndexSegStatus, string(post.status))
		post.status = ""
		return idx, post, true
	case post.risk != "":
		idx := secondaryIndexKey(tenant, redisIndexSegRisk, string(post.risk))
		post.risk = ""
		return idx, post, true
	case post.agentProduct != "":
		idx := secondaryIndexKey(tenant, redisIndexSegAgent, post.agentProduct)
		post.agentProduct = ""
		return idx, post, true
	case post.owner != "":
		idx := secondaryIndexKey(tenant, redisIndexSegOwner, post.owner)
		post.owner = ""
		return idx, post, true
	default:
		return tenantIndexKey(tenant), post, true
	}
}

type postFilterSet struct {
	status       FindingStatus
	risk         FindingRisk
	agentProduct string
	owner        string

	// EDGE-143.5 — §10.2 dimensions left over after chooseIndex selects
	// the primary index. nil/zero means "no filter on this dimension".
	sourceType         string
	clusterID          string
	namespace          string
	ciProvider         string
	repo               string
	signals            []string
	confidenceMin      float64
	firstSeenAfter     *time.Time
	lastSeenBefore     *time.Time
	exceptionID        string
	includeManagedSkip bool
}

func matchesPostFilters(f *ShadowAgentFinding, p postFilterSet) bool {
	if p.status != "" && f.Status != p.status {
		return false
	}
	if p.risk != "" && f.Risk != p.risk {
		return false
	}
	if p.agentProduct != "" && f.AgentProduct != p.agentProduct {
		return false
	}
	if p.owner != "" && f.OwnerPrincipalID != p.owner {
		return false
	}
	if p.sourceType != "" && f.SourceType != p.sourceType {
		return false
	}
	if p.clusterID != "" && f.ClusterID != p.clusterID {
		return false
	}
	if p.namespace != "" && f.Namespace != p.namespace {
		return false
	}
	if p.ciProvider != "" && f.CIProvider != p.ciProvider {
		return false
	}
	if p.repo != "" && f.Repo != p.repo {
		return false
	}
	if p.exceptionID != "" && f.ExceptionID != p.exceptionID {
		return false
	}
	if p.confidenceMin > 0 && f.Confidence < p.confidenceMin {
		return false
	}
	if p.firstSeenAfter != nil {
		if f.FirstSeen == nil || !f.FirstSeen.After(*p.firstSeenAfter) {
			return false
		}
	}
	if p.lastSeenBefore != nil {
		if f.LastSeen == nil || !f.LastSeen.Before(*p.lastSeenBefore) {
			return false
		}
	}
	if len(p.signals) > 0 {
		if !anySignalMatches(f.SignalSet, p.signals) {
			return false
		}
	}
	// §10.3 managed_skip findings (FalsePositiveReason populated) are
	// excluded by default; opt-in with IncludeManagedSkip=true. §10.3
	// auto-promotion to managed_skip via exception_id is EDGE-143.6
	// scope; this task wires the filter only.
	if !p.includeManagedSkip && f.FalsePositiveReason != "" {
		return false
	}
	return true
}

// anySignalMatches reports whether any element of needles is present in
// haystack (set-intersection-non-empty). Both lists are bounded ≤16 by
// validation; O(N*M) is fine for that scale.
func anySignalMatches(haystack, needles []string) bool {
	for _, n := range needles {
		for _, h := range haystack {
			if h == n {
				return true
			}
		}
	}
	return false
}

// normalizeSignals lowercases + trims input signals, deduping in
// stable order. Returns nil for empty input.
func normalizeSignals(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

type zMember struct {
	member string
	score  float64
}

// zScanDescending pulls index members in descending score order with
// cursor support. Wraps ZREVRANGEBYSCORE so consumers don't have to
// build the Redis args by hand.
func (s *RedisStore) zScanDescending(
	ctx context.Context,
	key string,
	startScore float64,
	startID string,
	limit int,
) ([]zMember, error) {
	max := "+inf"
	if startScore > 0 {
		// Use exclusive max so the cursor entry itself is skipped — the
		// caller has already seen it.
		max = "(" + formatFloat(startScore)
	}
	if startID != "" && startScore > 0 {
		// startID + startScore together form the cursor: same-score
		// entries are ordered lexicographically by member, so we may
		// need a follow-up scan at exactly startScore for entries
		// after startID. We accept a slight over-scan by pulling
		// (startScore .. -inf] inclusive and post-filtering the
		// startScore tie. Simpler than ZRANGEBYLEX gymnastics; the
		// over-scan is bounded by limit*overScanFactor.
		max = "(" + formatFloat(startScore+1)
	}
	cmd := s.client.ZRevRangeByScoreWithScores(ctx, key, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    max,
		Offset: 0,
		Count:  int64(limit),
	})
	pairs, err := cmd.Result()
	if err != nil {
		return nil, err
	}
	out := make([]zMember, 0, len(pairs))
	skipping := startID != "" && startScore > 0
	for _, p := range pairs {
		member, _ := p.Member.(string)
		if skipping {
			if p.Score == startScore && member == startID {
				skipping = false
				continue
			}
			if p.Score < startScore {
				skipping = false
			}
		}
		out = append(out, zMember{member: member, score: p.Score})
	}
	return out, nil
}

func formatFloat(f float64) string {
	return fmt.Sprintf("%.6f", f)
}

func encodeCursor(score float64, id string) string {
	return fmt.Sprintf("%.6f:%s", score, id)
}

func decodeCursor(c string) (float64, string, error) {
	c = strings.TrimSpace(c)
	if c == "" {
		return 0, "", nil
	}
	idx := strings.IndexByte(c, ':')
	if idx <= 0 || idx == len(c)-1 {
		return 0, "", ErrInvalidCursor
	}
	var score float64
	if _, err := fmt.Sscanf(c[:idx], "%f", &score); err != nil {
		return 0, "", ErrInvalidCursor
	}
	return score, c[idx+1:], nil
}
