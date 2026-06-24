package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// sessionKeyPrefix yields copilot:session:<tenant>:<id>. The tenant is part
	// of the key so a cross-tenant session-id guess lands on a different key and
	// returns ErrNotFound rather than another tenant's transcript.
	sessionKeyPrefix = "copilot:session:"
	// sessionIndexPrefix yields copilot:sessions:<tenant>:<user> (a ZSET scored
	// by updatedAt) so a future list endpoint can enumerate a user's sessions.
	sessionIndexPrefix = "copilot:sessions:"

	defaultSessionTTL  = 90 * 24 * time.Hour
	defaultMaxMessages = 1000
	appendMaxRetries   = 8

	envSessionTTLHours = "COPILOT_SESSION_TTL_HOURS"
	envMaxMessages     = "COPILOT_SESSION_MAX_MESSAGES"
)

// storedSession wraps the wire CopilotSession with its owning tenant. The wire
// shape (CopilotSession) is left untouched so the read handler and SDK models
// need no changes.
type storedSession struct {
	Tenant  string         `json:"tenant"`
	Session CopilotSession `json:"session"`
}

// RedisStore is the production, Redis-backed copilot.Store. Reads are
// tenant-scoped (operator-readable within a tenant); writes append messages
// atomically via an optimistic WATCH/MULTI transaction.
type RedisStore struct {
	client      redis.UniversalClient
	ttl         time.Duration
	maxMessages int
}

// NewRedisStore builds a RedisStore from a shared client. Returns nil when the
// client is nil so the caller can fall back to NotImplementedStore.
func NewRedisStore(client redis.UniversalClient) *RedisStore {
	if client == nil {
		return nil
	}
	return &RedisStore{
		client:      client,
		ttl:         sessionTTLFromEnv(),
		maxMessages: maxMessagesFromEnv(),
	}
}

func sessionKey(tenant, id string) string       { return sessionKeyPrefix + tenant + ":" + id }
func sessionIndexKey(tenant, user string) string { return sessionIndexPrefix + tenant + ":" + user }

// GetSession returns the session if it exists in the caller's tenant. Missing
// sessions (and cross-tenant key misses) return ErrNotFound; a value whose
// stored tenant somehow disagrees with the request returns ErrCrossTenant.
func (s *RedisStore) GetSession(ctx context.Context, tenant, sessionID, userID string) (*CopilotSession, error) {
	if s == nil || s.client == nil {
		return nil, ErrNotImplemented
	}
	tenant = strings.TrimSpace(tenant)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ErrNotFound
	}
	data, err := s.client.Get(ctx, sessionKey(tenant, sessionID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("copilot store: get session: %w", err)
	}
	var stored storedSession
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("copilot store: decode session: %w", err)
	}
	// Defense in depth — the key is already tenant-scoped.
	if strings.TrimSpace(stored.Tenant) != tenant {
		return nil, ErrCrossTenant
	}
	sess := stored.Session
	return &sess, nil
}

// AppendMessage records one transcript entry, creating the session on first
// write. It is idempotent on msg.ID (so audit-stream replays do not duplicate
// entries) and atomic under concurrent appends via WATCH/MULTI.
func (s *RedisStore) AppendMessage(ctx context.Context, tenant, userID, sessionID string, msg CopilotMessage) error {
	if s == nil || s.client == nil {
		return ErrNotImplemented
	}
	tenant = strings.TrimSpace(tenant)
	sessionID = strings.TrimSpace(sessionID)
	if tenant == "" || sessionID == "" {
		return fmt.Errorf("copilot store: tenant and session id required")
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}
	if strings.TrimSpace(msg.ID) == "" {
		msg.ID = messageID(tenant, sessionID, msg)
	}
	key := sessionKey(tenant, sessionID)

	txf := func(tx *redis.Tx) error {
		stored := storedSession{Tenant: tenant, Session: CopilotSession{ID: sessionID, UserID: userID, CreatedAt: msg.Timestamp}}
		data, err := tx.Get(ctx, key).Bytes()
		switch {
		case errors.Is(err, redis.Nil):
			// new session — use the initialized value above
		case err != nil:
			return err
		default:
			if err := json.Unmarshal(data, &stored); err != nil {
				return err
			}
		}
		// Idempotency: a replayed audit event must not duplicate a message.
		for _, m := range stored.Session.Messages {
			if m.ID == msg.ID {
				return nil
			}
		}
		stored.Tenant = tenant
		if stored.Session.ID == "" {
			stored.Session.ID = sessionID
		}
		if stored.Session.UserID == "" {
			stored.Session.UserID = userID
		}
		if stored.Session.CreatedAt.IsZero() {
			stored.Session.CreatedAt = msg.Timestamp
		}
		stored.Session.Messages = append(stored.Session.Messages, msg)
		if s.maxMessages > 0 && len(stored.Session.Messages) > s.maxMessages {
			// keep the most recent messages
			stored.Session.Messages = stored.Session.Messages[len(stored.Session.Messages)-s.maxMessages:]
		}
		stored.Session.UpdatedAt = msg.Timestamp
		if strings.TrimSpace(stored.Session.Title) == "" {
			stored.Session.Title = deriveTitle(msg.Content)
		}
		encoded, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, encoded, s.ttl)
			if userID != "" {
				idxKey := sessionIndexKey(tenant, userID)
				pipe.ZAdd(ctx, idxKey, redis.Z{Score: float64(msg.Timestamp.UnixNano()), Member: sessionID})
				pipe.Expire(ctx, idxKey, s.ttl)
			}
			return nil
		})
		return err
	}

	for i := 0; i < appendMaxRetries; i++ {
		err := s.client.Watch(ctx, txf, key)
		if err == nil {
			return nil
		}
		if errors.Is(err, redis.TxFailedErr) {
			continue // optimistic-lock contention — retry
		}
		return fmt.Errorf("copilot store: append message: %w", err)
	}
	return fmt.Errorf("copilot store: append message: write contention after %d retries", appendMaxRetries)
}

// messageID derives a deterministic id so at-least-once audit delivery is
// idempotent: the same tool invocation produces the same message id.
func messageID(tenant, sessionID string, msg CopilotMessage) string {
	h := sha256.New()
	// hash.Hash.Write never returns an error; ignore explicitly for errcheck.
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%d|%s", tenant, sessionID, msg.Role, msg.Timestamp.UnixNano(), msg.Content)
	for _, j := range msg.JobIDs {
		_, _ = fmt.Fprintf(h, "|%s", j)
	}
	return "msg-" + hex.EncodeToString(h.Sum(nil))[:24]
}

func deriveTitle(content string) string {
	t := strings.TrimSpace(content)
	if t == "" {
		return ""
	}
	if len(t) > 80 {
		t = t[:80]
	}
	return t
}

func sessionTTLFromEnv() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(envSessionTTLHours)); raw != "" {
		if h, err := strconv.Atoi(raw); err == nil && h > 0 {
			return time.Duration(h) * time.Hour
		}
	}
	return defaultSessionTTL
}

func maxMessagesFromEnv() int {
	if raw := strings.TrimSpace(os.Getenv(envMaxMessages)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxMessages
}
