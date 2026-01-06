package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

// DLQStore persists DLQ entries in Redis.
type DLQStore struct {
	client *redis.Client
}

func NewDLQStore(url string) (*DLQStore, error) {
	if url == "" {
		url = defaultRedisURL
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	return &DLQStore{client: client}, nil
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
	pipe.Set(ctx, dlqEntryKey(entry.JobID), data, 0)
	pipe.ZAdd(ctx, dlqIndexKey(), redis.Z{Score: float64(entry.CreatedAt.Unix()), Member: entry.JobID})
	pipe.ZRemRangeByRank(ctx, dlqIndexKey(), 0, -1001) // keep last ~1000
	_, err = pipe.Exec(ctx)
	return err
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
	_, _ = pipe.Exec(ctx)

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
	_, _ = pipe.Exec(ctx)

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
