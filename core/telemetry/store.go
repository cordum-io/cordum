package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const (
	snapshotLatestKey   = "cordum:telemetry:snapshot:last"
	snapshotKeyPrefix   = "cordum:telemetry:snapshot:"
	historyKey          = "cordum:telemetry:history"
	lastReportStatusKey = "cordum:telemetry:last_report"
	collectorLockKey    = "cordum:telemetry:collector:lock"
	consentModeKey      = "cordum:telemetry:consent:mode"
	defaultRetention    = 30 * 24 * time.Hour
	defaultMaxHistory   = 180
)

type ReportStatus struct {
	ReportedAt time.Time `json:"reported_at"`
	Endpoint   string    `json:"endpoint,omitempty"`
	Success    bool      `json:"success"`
}

// Store persists telemetry payloads and report metadata in Redis.
type Store struct {
	client     redis.UniversalClient
	retention  time.Duration
	maxHistory int64
}

func NewStore(redisURL string) (*Store, error) {
	client, err := redisutil.NewClient(redisURL)
	if err != nil {
		return nil, fmt.Errorf("create telemetry redis client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect telemetry redis client: %w", err)
	}
	return NewStoreWithClient(client), nil
}

func NewStoreWithClient(client redis.UniversalClient) *Store {
	return &Store{
		client:     client,
		retention:  defaultRetention,
		maxHistory: defaultMaxHistory,
	}
}

func (s *Store) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

// GetConsentMode reads the operator's persisted telemetry consent from Redis.
// Returns empty string if no consent has been recorded.
func (s *Store) GetConsentMode(ctx context.Context) (string, error) {
	if s == nil || s.client == nil {
		return "", nil
	}
	val, err := s.client.Get(ctx, consentModeKey).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

// SetConsentMode persists the operator's telemetry consent choice in Redis.
func (s *Store) SetConsentMode(ctx context.Context, mode string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("telemetry store unavailable")
	}
	return s.client.Set(ctx, consentModeKey, mode, 0).Err()
}

func (s *Store) SaveSnapshot(ctx context.Context, payload TelemetryPayload) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("telemetry store unavailable")
	}
	if payload.CollectedAt.IsZero() {
		payload.CollectedAt = time.Now().UTC()
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telemetry payload: %w", err)
	}
	snapshotKey := snapshotKey(payload.CollectedAt)
	cutoff := time.Now().UTC().Add(-s.retention)

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, snapshotLatestKey, data, 0)
	pipe.Set(ctx, snapshotKey, data, s.retention)
	pipe.ZAdd(ctx, historyKey, redis.Z{
		Score:  float64(payload.CollectedAt.Unix()),
		Member: snapshotKey,
	})
	pipe.ZRemRangeByScore(ctx, historyKey, "-inf", strconv.FormatInt(cutoff.Unix(), 10))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("save telemetry snapshot: %w", err)
	}

	if err := s.trimHistory(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) InspectPayload(ctx context.Context) (*TelemetryPayload, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("telemetry store unavailable")
	}
	data, err := s.client.Get(ctx, snapshotLatestKey).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read telemetry snapshot: %w", err)
	}
	var payload TelemetryPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal telemetry snapshot: %w", err)
	}
	return &payload, nil
}

func (s *Store) ExportPayload(ctx context.Context) ([]byte, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("telemetry store unavailable")
	}
	data, err := s.client.Get(ctx, snapshotLatestKey).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("export telemetry snapshot: %w", err)
	}
	return data, nil
}

func (s *Store) History(ctx context.Context, limit int64) ([]TelemetryPayload, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("telemetry store unavailable")
	}
	if limit <= 0 {
		limit = 20
	}
	keys, err := s.client.ZRevRange(ctx, historyKey, 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("list telemetry history: %w", err)
	}
	if len(keys) == 0 {
		return []TelemetryPayload{}, nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.Get(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("read telemetry history: %w", err)
	}

	out := make([]TelemetryPayload, 0, len(keys))
	for _, cmd := range cmds {
		data, err := cmd.Bytes()
		if err != nil {
			continue
		}
		var payload TelemetryPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			continue
		}
		out = append(out, payload)
	}
	return out, nil
}

func (s *Store) SaveReportStatus(ctx context.Context, status ReportStatus) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("telemetry store unavailable")
	}
	if status.ReportedAt.IsZero() {
		status.ReportedAt = time.Now().UTC()
	}
	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal telemetry report status: %w", err)
	}
	if err := s.client.Set(ctx, lastReportStatusKey, data, 0).Err(); err != nil {
		return fmt.Errorf("save telemetry report status: %w", err)
	}
	return nil
}

func (s *Store) LastReportStatus(ctx context.Context) (*ReportStatus, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("telemetry store unavailable")
	}
	data, err := s.client.Get(ctx, lastReportStatusKey).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read telemetry report status: %w", err)
	}
	var status ReportStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("unmarshal telemetry report status: %w", err)
	}
	return &status, nil
}

func (s *Store) trimHistory(ctx context.Context) error {
	if s.maxHistory <= 0 {
		return nil
	}
	total, err := s.client.ZCard(ctx, historyKey).Result()
	if err != nil {
		return fmt.Errorf("count telemetry history: %w", err)
	}
	extra := total - s.maxHistory
	if extra <= 0 {
		return nil
	}
	keys, err := s.client.ZRange(ctx, historyKey, 0, extra-1).Result()
	if err != nil {
		return fmt.Errorf("trim telemetry history keys: %w", err)
	}
	if len(keys) == 0 {
		return nil
	}
	pipe := s.client.TxPipeline()
	pipe.ZRem(ctx, historyKey, toAnySlice(keys)...)
	pipe.Del(ctx, keys...)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("trim telemetry history: %w", err)
	}
	return nil
}

func snapshotKey(collectedAt time.Time) string {
	return snapshotKeyPrefix + strconv.FormatInt(collectedAt.UTC().UnixMilli(), 10)
}

func toAnySlice(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
