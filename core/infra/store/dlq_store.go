package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

// DLQEntry captures a failed job/result for diagnostics.
type DLQEntry struct {
	JobID      string    `json:"job_id"`
	Topic      string    `json:"topic,omitempty"`
	Status     string    `json:"status,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	ReasonCode string    `json:"reason_code,omitempty"`
	LastState  string    `json:"last_state,omitempty"`
	Attempts   int       `json:"attempts,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

const (
	defaultDLQEntryTTL = 30 * 24 * time.Hour
	dlqEntryTTLDaysEnv = "CORDUM_DLQ_ENTRY_TTL_DAYS"
	dlqMaxEntries      = 1000
)

// DLQStore persists DLQ entries in Redis.
type DLQStore struct {
	client   redis.UniversalClient
	entryTTL time.Duration
}

func NewDLQStore(url string, entryTTL time.Duration) (*DLQStore, error) {
	if url == "" {
		url = defaultRedisURL
	}
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	return &DLQStore{client: client, entryTTL: resolveDLQEntryTTL(entryTTL)}, nil
}

func (s *DLQStore) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// Add appends an entry and maintains a sorted index.
func (s *DLQStore) Add(ctx context.Context, entry DLQEntry) error {
	if entry.JobID == "" {
		return fmt.Errorf("job id required")
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal dlq entry: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, dlqEntryKey(entry.JobID), data, s.entryTTL)
	pipe.ZAdd(ctx, dlqIndexKey(), redis.Z{Score: float64(entry.CreatedAt.Unix()), Member: entry.JobID})
	trimCmd := pipe.ZRange(ctx, dlqIndexKey(), 0, -(dlqMaxEntries + 1))
	remCmd := pipe.ZRemRangeByRank(ctx, dlqIndexKey(), 0, -(dlqMaxEntries + 1)) // keep last ~1000
	if _, err = pipe.Exec(ctx); err != nil {
		return err
	}
	removed, err := remCmd.Result()
	if err != nil {
		return err
	}
	if removed == 0 {
		return nil
	}
	trimIDs, err := trimCmd.Result()
	if err != nil {
		return err
	}
	if len(trimIDs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(trimIDs))
	for _, id := range trimIDs {
		keys = append(keys, dlqEntryKey(id))
	}
	return s.client.Del(ctx, keys...).Err()
}

// List returns recent DLQ entries. Stale index entries (whose data keys have
// expired) are lazily removed from the index.
func (s *DLQStore) List(ctx context.Context, limit int64) ([]DLQEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	ids, err := s.client.ZRevRange(ctx, dlqIndexKey(), 0, limit-1).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []DLQEntry{}, nil
	}
	pipe := s.client.Pipeline()
	cmds := make(map[string]*redis.StringCmd, len(ids))
	for _, id := range ids {
		cmds[id] = pipe.Get(ctx, dlqEntryKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		slog.Warn("redis pipeline exec", "op", "list_dlq", "error", err)
	}

	out := make([]DLQEntry, 0, len(ids))
	var staleIDs []string
	for _, id := range ids {
		cmd := cmds[id]
		if cmd == nil {
			continue
		}
		data, cmdErr := cmd.Bytes()
		if cmdErr != nil {
			if cmdErr == redis.Nil {
				staleIDs = append(staleIDs, id)
			} else {
				slog.Warn("dlq-store: pipeline get error", "id", id, "error", cmdErr)
			}
			continue
		}
		var e DLQEntry
		if err := json.Unmarshal(data, &e); err != nil {
			slog.Warn("dlq-store: corrupt entry skipped", "id", id, "error", err)
			continue
		}
		out = append(out, e)
	}
	// Lazy cleanup: remove index entries whose data keys have expired.
	if len(staleIDs) > 0 {
		members := make([]interface{}, len(staleIDs))
		for i, id := range staleIDs {
			members[i] = id
		}
		if remErr := s.client.ZRem(ctx, dlqIndexKey(), members...).Err(); remErr != nil {
			slog.Warn("dlq-store: lazy cleanup failed", "stale_count", len(staleIDs), "error", remErr)
		} else {
			slog.Info("dlq-store: lazy cleanup removed stale index entries", "count", len(staleIDs))
		}
	}
	return out, nil
}

// ListByScore returns DLQ entries before the given cursor timestamp (unix seconds).
// Stale index entries are lazily cleaned up.
func (s *DLQStore) ListByScore(ctx context.Context, cursorUnix int64, limit int64) ([]DLQEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if cursorUnix <= 0 {
		cursorUnix = time.Now().UTC().Unix()
	}
	ids, err := s.client.ZRevRangeByScore(ctx, dlqIndexKey(), &redis.ZRangeBy{
		Max:    fmt.Sprintf("%d", cursorUnix),
		Min:    "-inf",
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []DLQEntry{}, nil
	}
	pipe := s.client.Pipeline()
	cmds := make(map[string]*redis.StringCmd, len(ids))
	for _, id := range ids {
		cmds[id] = pipe.Get(ctx, dlqEntryKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		slog.Warn("redis pipeline exec", "op", "list_dlq_by_score", "error", err)
	}

	out := make([]DLQEntry, 0, len(ids))
	var staleIDs []string
	for _, id := range ids {
		cmd := cmds[id]
		if cmd == nil {
			continue
		}
		data, cmdErr := cmd.Bytes()
		if cmdErr != nil {
			if cmdErr == redis.Nil {
				staleIDs = append(staleIDs, id)
			} else {
				slog.Warn("dlq-store: pipeline get error", "id", id, "error", cmdErr)
			}
			continue
		}
		var e DLQEntry
		if err := json.Unmarshal(data, &e); err != nil {
			slog.Warn("dlq-store: corrupt entry skipped", "id", id, "error", err)
			continue
		}
		out = append(out, e)
	}
	// Lazy cleanup: remove index entries whose data keys have expired.
	if len(staleIDs) > 0 {
		members := make([]interface{}, len(staleIDs))
		for i, id := range staleIDs {
			members[i] = id
		}
		if remErr := s.client.ZRem(ctx, dlqIndexKey(), members...).Err(); remErr != nil {
			slog.Warn("dlq-store: lazy cleanup failed", "stale_count", len(staleIDs), "error", remErr)
		} else {
			slog.Info("dlq-store: lazy cleanup removed stale index entries", "count", len(staleIDs))
		}
	}
	return out, nil
}

// Get returns a single DLQ entry.
func (s *DLQStore) Get(ctx context.Context, jobID string) (*DLQEntry, error) {
	if jobID == "" {
		return nil, fmt.Errorf("job id required")
	}
	data, err := s.client.Get(ctx, dlqEntryKey(jobID)).Bytes()
	if err != nil {
		return nil, err
	}
	var e DLQEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// Delete removes an entry.
func (s *DLQStore) Delete(ctx context.Context, jobID string) error {
	if jobID == "" {
		return fmt.Errorf("job id required")
	}
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, dlqEntryKey(jobID))
	pipe.ZRem(ctx, dlqIndexKey(), jobID)
	_, err := pipe.Exec(ctx)
	return err
}

func dlqEntryKey(jobID string) string {
	return "dlq:entry:" + jobID
}

func dlqIndexKey() string {
	return "dlq:index"
}

func resolveDLQEntryTTL(entryTTL time.Duration) time.Duration {
	if entryTTL > 0 {
		return entryTTL
	}
	if envTTL := dlqEntryTTLFromEnv(); envTTL > 0 {
		return envTTL
	}
	return defaultDLQEntryTTL
}

func dlqEntryTTLFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv(dlqEntryTTLDaysEnv))
	if raw == "" {
		return 0
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("invalid "+dlqEntryTTLDaysEnv+", using default", "value", sanitizeLogValue(raw), "error", sanitizeLogValue(err.Error()), "default", defaultDLQEntryTTL)
		return 0
	}
	if days <= 0 {
		slog.Warn("non-positive "+dlqEntryTTLDaysEnv+", using default", "value", days, "default", defaultDLQEntryTTL)
		return 0
	}
	return time.Duration(days) * 24 * time.Hour
}

// ---------------------------------------------------------------------------
// Index cleanup
// ---------------------------------------------------------------------------

// cleanupBatchSize limits how many EXISTS commands are issued per pipeline
// during stale entry cleanup to avoid large pipeline bursts.
const cleanupBatchSize = 500

// CleanupStaleEntries scans the DLQ index and removes members whose data keys
// (dlq:entry:<id>) no longer exist (expired via TTL). Returns the number of
// stale entries removed.
func (s *DLQStore) CleanupStaleEntries(ctx context.Context) (int64, error) {
	ids, err := s.client.ZRange(ctx, dlqIndexKey(), 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("dlq cleanup zrange: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	var totalRemoved int64
	// Process in batches to bound pipeline size.
	for start := 0; start < len(ids); start += cleanupBatchSize {
		end := start + cleanupBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]

		pipe := s.client.Pipeline()
		existsCmds := make(map[string]*redis.IntCmd, len(batch))
		for _, id := range batch {
			existsCmds[id] = pipe.Exists(ctx, dlqEntryKey(id))
		}
		if _, pipeErr := pipe.Exec(ctx); pipeErr != nil && pipeErr != redis.Nil {
			slog.Warn("dlq-store: cleanup pipeline exec error", "error", pipeErr)
			continue
		}

		var stale []interface{}
		for _, id := range batch {
			cmd := existsCmds[id]
			if cmd == nil {
				continue
			}
			exists, cmdErr := cmd.Result()
			if cmdErr != nil {
				slog.Warn("dlq-store: cleanup exists error", "id", id, "error", cmdErr)
				continue
			}
			if exists == 0 {
				stale = append(stale, id)
			}
		}
		if len(stale) == 0 {
			continue
		}
		removed, remErr := s.client.ZRem(ctx, dlqIndexKey(), stale...).Result()
		if remErr != nil {
			slog.Warn("dlq-store: cleanup zrem error", "error", remErr)
			continue
		}
		totalRemoved += removed
	}

	if totalRemoved > 0 {
		slog.Info("dlq-store: cleaned up stale index entries", "removed", totalRemoved)
	}
	return totalRemoved, nil
}

// StartCleanupLoop runs CleanupStaleEntries periodically until ctx is cancelled.
// The goroutine exits cleanly when the context is done.
func (s *DLQStore) StartCleanupLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				removed, err := s.CleanupStaleEntries(ctx)
				if err != nil {
					slog.Warn("dlq-store: periodic cleanup error", "error", err)
				} else if removed > 0 {
					slog.Info("dlq-store: periodic cleanup completed", "removed", removed)
				}
			}
		}
	}()
}
