package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const maxLockResults = 500

// LockEntry describes a single active distributed lock.
type LockEntry struct {
	Key            string `json:"key"`
	Holder         string `json:"holder"`
	TTLRemainingMs int64  `json:"ttl_remaining_ms"`
	Type           string `json:"type"`
}

// lockPrefixTypes maps known Redis lock key prefixes to human-readable types.
var lockPrefixTypes = []struct {
	prefix string
	typ    string
}{
	{"cordum:reconciler:", "reconciler"},
	{"cordum:replayer:", "replayer"},
	{"cordum:scheduler:job:", "job"},
	{"cordum:scheduler:snapshot:", "snapshot"},
	{"cordum:dlq:cleanup", "dlq_cleanup"},
	{"cordum:wf:run:lock:", "workflow_run"},
	{"cordum:wf:delay:poller", "delay_poller"},
	{"cordum:workflow-engine:reconciler:", "workflow_reconciler"},
	{"cordum:rl:", "rate_limit"},
	{"cordum:auth:jwks:", "jwks_cache"},
	{"cordum:cb:", "circuit_breaker"},
	{"cordum:cache:marketplace", "marketplace_cache"},
}

// classifyLockType returns the lock type for a Redis key based on known prefixes.
func classifyLockType(key string) string {
	for _, lp := range lockPrefixTypes {
		if strings.HasPrefix(key, lp.prefix) {
			return lp.typ
		}
	}
	return "unknown"
}

// scanLocks scans Redis for keys matching a pattern and returns lock entries.
// Uses SCAN (never KEYS) for production safety. Returns at most limit entries.
func scanLocks(ctx context.Context, rdb redis.UniversalClient, pattern string, limit int) ([]LockEntry, error) {
	if limit <= 0 {
		return nil, nil
	}

	var keys []string
	iter := rdb.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
		if len(keys) >= limit {
			break
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}

	pipe := rdb.Pipeline()
	getCmds := make([]*redis.StringCmd, len(keys))
	ttlCmds := make([]*redis.DurationCmd, len(keys))
	for i, key := range keys {
		getCmds[i] = pipe.Get(ctx, key)
		ttlCmds[i] = pipe.PTTL(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}

	entries := make([]LockEntry, 0, len(keys))
	for i, key := range keys {
		holder, err := getCmds[i].Result()
		if err != nil {
			continue // Key expired between SCAN and GET.
		}
		ttlMs := int64(0)
		if ttl, err := ttlCmds[i].Result(); err == nil && ttl > 0 {
			ttlMs = ttl.Milliseconds()
		}
		entries = append(entries, LockEntry{
			Key:            key,
			Holder:         holder,
			TTLRemainingMs: ttlMs,
			Type:           classifyLockType(key),
		})
	}
	return entries, nil
}

// handleAdminLocks returns all active distributed locks. Admin-only, read-only.
func (s *server) handleAdminLocks(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}

	if s.jobStore == nil {
		http.Error(w, `{"error":"redis unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rdb := s.jobStore.Client()
	allLocks := make([]LockEntry, 0)

	for _, lp := range lockPrefixTypes {
		remaining := maxLockResults - len(allLocks)
		if remaining <= 0 {
			break
		}
		pattern := lp.prefix
		// For exact keys (no wildcard suffix), match exactly.
		if !strings.HasSuffix(pattern, ":") {
			pattern += "*"
		} else {
			pattern += "*"
		}
		entries, err := scanLocks(ctx, rdb, pattern, remaining)
		if err != nil {
			continue // Best-effort: skip prefixes that fail.
		}
		allLocks = append(allLocks, entries...)
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"locks": allLocks})
}
