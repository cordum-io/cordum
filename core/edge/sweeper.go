package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// DefaultSessionRetentionTTL is the default age after which Edge sessions
	// are eligible for background cleanup.
	DefaultSessionRetentionTTL = 30 * 24 * time.Hour
	// DefaultSessionSweepInterval is the default cadence for retention sweeps.
	DefaultSessionSweepInterval = time.Hour

	sessionSweepScanCount = 100
)

// SessionSweeperOptions configures the retention sweeper.
type SessionSweeperOptions struct {
	RetentionTTL time.Duration
	Interval     time.Duration
	Now          func() time.Time
}

// SessionSweeper removes Edge sessions that are older than the configured
// retention TTL. It delegates deletion to RedisStore.DeleteSession so cleanup
// inherits bounded ZSCAN + batched DEL behavior.
type SessionSweeper struct {
	store        *RedisStore
	retentionTTL time.Duration
	interval     time.Duration
	now          func() time.Time
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
	return &SessionSweeper{store: store, retentionTTL: retentionTTL, interval: interval, now: now}, nil
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

// SweepOnce scans session keys in bounded pages and deletes eligible sessions.
func (s *SessionSweeper) SweepOnce(ctx context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("edge session sweeper unavailable")
	}
	if err := s.store.ensureReady(); err != nil {
		return 0, err
	}
	var swept int
	var cursor uint64
	for {
		if err := ctx.Err(); err != nil {
			return swept, err
		}
		keys, nextCursor, err := s.store.client.Scan(ctx, cursor, "edge:session:*", sessionSweepScanCount).Result()
		if err != nil {
			return swept, fmt.Errorf("scan edge sessions for retention: %w", err)
		}
		count, err := s.sweepSessionKeys(ctx, keys)
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

func (s *SessionSweeper) sweepSessionKeys(ctx context.Context, keys []string) (int, error) {
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
