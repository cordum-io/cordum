package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/redis/go-redis/v9"
)

const (
	// DefaultSessionRetentionTTL is the default age after which Edge sessions
	// are eligible for background cleanup.
	DefaultSessionRetentionTTL = 30 * 24 * time.Hour
	// DefaultSessionSweepInterval is the default cadence for retention sweeps.
	DefaultSessionSweepInterval = time.Hour
	// DefaultApprovalSweepInterval is the default cadence for approval expiry
	// sweeps. It keeps approval indexes bounded even when nobody GETs a stale
	// approval directly.
	DefaultApprovalSweepInterval = 30 * time.Second

	sessionSweepScanCount          = 100
	approvalSweepScanCount         = 100
	defaultApprovalSweepMaxTenants = 100
)

// SessionSweeperOptions configures the retention sweeper.
type SessionSweeperOptions struct {
	RetentionTTL time.Duration
	Interval     time.Duration
	Now          func() time.Time
	// LegalHolds is checked before every deletion: sessions of a tenant
	// under an active audit legal hold are kept, and so are those of a
	// tenant whose hold check fails. Nil keeps every session.
	LegalHolds *audit.LegalHoldStore
}

// SessionSweeper removes Edge sessions that are older than the configured
// retention TTL, except for tenants under legal hold. It delegates deletion
// to RedisStore.DeleteSession so cleanup inherits bounded ZSCAN + batched
// DEL behavior.
type SessionSweeper struct {
	store        *RedisStore
	retentionTTL time.Duration
	interval     time.Duration
	now          func() time.Time
	holds        *audit.LegalHoldStore
}

// errSessionSweepNoLegalHolds stops a sweep that cannot check legal holds.
var errSessionSweepNoLegalHolds = errors.New("edge session sweeper: legal hold store unavailable; nothing deleted")

// ApprovalSweeperOptions configures the periodic approval-expiry sweeper.
type ApprovalSweeperOptions struct {
	Interval           time.Duration
	Now                func() time.Time
	MaxTenantsPerSweep int
}

// ApprovalSweeper expires stale pending approvals across tenants on a bounded
// interval. It delegates state transitions to RedisStore.ExpireApprovals so
// index cleanup and CAS behavior stay in one store primitive.
type ApprovalSweeper struct {
	store              *RedisStore
	interval           time.Duration
	now                func() time.Time
	maxTenantsPerSweep int
}

// NewSessionSweeper validates and builds a retention sweeper.
func NewSessionSweeper(store *RedisStore, opts SessionSweeperOptions) (*SessionSweeper, error) {
	if store == nil {
		return nil, fmt.Errorf("edge session sweeper requires redis store")
	}
	retentionTTL := opts.RetentionTTL
	if retentionTTL == 0 {
		retentionTTL = DefaultSessionRetentionTTL
	}
	if retentionTTL < 0 {
		return nil, fmt.Errorf("edge session retention TTL must be positive")
	}
	interval := opts.Interval
	if interval == 0 {
		interval = DefaultSessionSweepInterval
	}
	if interval < 0 {
		return nil, fmt.Errorf("edge session sweep interval must be positive")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &SessionSweeper{store: store, retentionTTL: retentionTTL, interval: interval, now: now, holds: opts.LegalHolds}, nil
}

// NewApprovalSweeper validates and builds an approval-expiry sweeper.
func NewApprovalSweeper(store *RedisStore, opts ApprovalSweeperOptions) (*ApprovalSweeper, error) {
	if store == nil {
		return nil, fmt.Errorf("edge approval sweeper requires redis store")
	}
	interval := opts.Interval
	if interval == 0 {
		interval = DefaultApprovalSweepInterval
	}
	if interval < 0 {
		return nil, fmt.Errorf("edge approval sweep interval must be positive")
	}
	maxTenants := opts.MaxTenantsPerSweep
	if maxTenants == 0 {
		maxTenants = defaultApprovalSweepMaxTenants
	}
	if maxTenants < 0 {
		return nil, fmt.Errorf("edge approval sweep tenant cap must be positive")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ApprovalSweeper{store: store, interval: interval, now: now, maxTenantsPerSweep: maxTenants}, nil
}

// Run sweeps immediately, then on each interval until ctx is cancelled.
func (s *SessionSweeper) Run(ctx context.Context) {
	if s == nil {
		return
	}
	_, _ = s.SweepOnce(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.SweepOnce(ctx)
		}
	}
}

// Run sweeps immediately, then on each interval until ctx is cancelled.
func (s *ApprovalSweeper) Run(ctx context.Context) {
	if s == nil {
		return
	}
	_, _ = s.SweepOnce(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.SweepOnce(ctx)
		}
	}
}

// SweepOnce scans session keys in bounded pages and deletes eligible sessions.
func (s *SessionSweeper) SweepOnce(ctx context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("edge session sweeper unavailable")
	}
	if err := s.store.ensureReady(); err != nil {
		return 0, err
	}
	if s.holds == nil {
		return 0, errSessionSweepNoLegalHolds
	}
	var swept int
	var cursor uint64
	logged := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return swept, err
		}
		keys, nextCursor, err := s.store.client.Scan(ctx, cursor, "edge:session:*", sessionSweepScanCount).Result()
		if err != nil {
			return swept, fmt.Errorf("scan edge sessions for retention: %w", err)
		}
		count, err := s.sweepSessionKeys(ctx, keys, logged)
		if err != nil {
			return swept, err
		}
		swept += count
		cursor = nextCursor
		if cursor == 0 {
			return swept, nil
		}
	}
}

// SweepOnce scans pending-approval status indexes, expires stale approvals per
// tenant, and emits latency + count metrics through the store recorder.
func (s *ApprovalSweeper) SweepOnce(ctx context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("edge approval sweeper unavailable")
	}
	if err := s.store.ensureReady(); err != nil {
		return 0, err
	}
	start := time.Now()
	expired, err := s.sweepPendingApprovalIndexes(ctx)
	if s.store.recorder != nil {
		s.store.recorder.ObserveApprovalSweepDuration(time.Since(start))
		s.store.recorder.AddApprovalSweepExpired(expired)
	}
	return expired, err
}

func (s *ApprovalSweeper) sweepPendingApprovalIndexes(ctx context.Context) (int, error) {
	var expired int
	var cursor uint64
	seen := map[string]struct{}{}
	for {
		if err := ctx.Err(); err != nil {
			return expired, err
		}
		keys, nextCursor, err := s.store.client.Scan(ctx, cursor, "edge:approvals:index:status:*:"+string(ApprovalStatusPending), approvalSweepScanCount).Result()
		if err != nil {
			return expired, fmt.Errorf("scan edge approval pending indexes: %w", err)
		}
		for _, key := range keys {
			if len(seen) >= s.maxTenantsPerSweep {
				return expired, nil
			}
			tenant, ok := tenantFromPendingApprovalStatusKey(key)
			if !ok {
				continue
			}
			if _, exists := seen[tenant]; exists {
				continue
			}
			seen[tenant] = struct{}{}
			n, err := s.store.ExpireApprovals(ctx, tenant, s.now().UTC())
			if err != nil {
				return expired, err
			}
			expired += n
		}
		cursor = nextCursor
		if cursor == 0 {
			return expired, nil
		}
	}
}

func (s *SessionSweeper) sweepSessionKeys(ctx context.Context, keys []string, logged map[string]bool) (int, error) {
	var swept int
	for _, key := range keys {
		if !isSweepSessionDocumentKey(key) {
			continue
		}
		session, ok, err := s.loadSweepSession(ctx, key)
		if err != nil {
			return swept, err
		}
		if !ok || !s.sessionExpired(session) {
			continue
		}
		// Checked per session, so a hold placed mid-sweep stops deletion.
		held, err := s.holds.IsUnderHold(ctx, session.TenantID)
		if err != nil || held {
			if !logged[session.TenantID] {
				logged[session.TenantID] = true
				slog.Warn("edge session retention: keeping tenant's expired sessions",
					"tenant_id", session.TenantID, "legal_hold", held, "hold_check_error", err)
			}
			continue
		}
		if err := s.store.DeleteSession(ctx, session.TenantID, session.SessionID); err != nil {
			return swept, err
		}
		s.store.recorder.RecordSessionSwept()
		swept++
	}
	return swept, nil
}

func isSweepSessionDocumentKey(key string) bool {
	key = strings.TrimSpace(key)
	return strings.HasPrefix(key, edgeSessionKey("")) &&
		!strings.HasPrefix(key, edgeSessionHeartbeatKey(""))
}

func tenantFromPendingApprovalStatusKey(key string) (string, bool) {
	key = strings.TrimSpace(key)
	prefix := "edge:approvals:index:status:"
	suffix := ":" + string(ApprovalStatusPending)
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
		return "", false
	}
	tenant := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
	if strings.TrimSpace(tenant) == "" {
		return "", false
	}
	return tenant, true
}

func (s *SessionSweeper) loadSweepSession(ctx context.Context, key string) (EdgeSession, bool, error) {
	raw, err := s.store.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return EdgeSession{}, false, nil
	}
	if err != nil {
		return EdgeSession{}, false, fmt.Errorf("load edge session %s for retention: %w", key, err)
	}
	var session EdgeSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return EdgeSession{}, false, fmt.Errorf("decode edge session %s for retention: %w", key, err)
	}
	if session.SessionID == "" || session.TenantID == "" {
		return EdgeSession{}, false, nil
	}
	return session, true, nil
}

func (s *SessionSweeper) sessionExpired(session EdgeSession) bool {
	anchor := session.StartedAt
	if session.EndedAt != nil {
		anchor = *session.EndedAt
	}
	if anchor.IsZero() {
		return false
	}
	return !anchor.UTC().Add(s.retentionTTL).After(s.now().UTC())
}
