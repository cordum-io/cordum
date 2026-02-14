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

// List returns recent DLQ entries.
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
	for _, id := range ids {
		cmd := cmds[id]
		if cmd == nil {
			continue
		}
		data, err := cmd.Bytes()
		if err != nil {
			continue
		}
		var e DLQEntry
		if err := json.Unmarshal(data, &e); err != nil {
			slog.Warn("dlq-store: corrupt entry skipped", "id", id, "error", err)
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ListByScore returns DLQ entries before the given cursor timestamp (unix seconds).
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
		slog.Warn("redis pipeline exec", "op", "list_dlq", "error", err)
	}

	out := make([]DLQEntry, 0, len(ids))
	for _, id := range ids {
		cmd := cmds[id]
		if cmd == nil {
			continue
		}
		data, err := cmd.Bytes()
		if err != nil {
			continue
		}
		var e DLQEntry
		if err := json.Unmarshal(data, &e); err != nil {
			slog.Warn("dlq-store: corrupt entry skipped", "id", id, "error", err)
			continue
		}
		out = append(out, e)
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
	if err != nil || days <= 0 {
		return 0
	}
	return time.Duration(days) * 24 * time.Hour
}
