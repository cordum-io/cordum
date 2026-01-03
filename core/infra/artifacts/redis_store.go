package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/yaront1111/coretex-os/core/infra/memory"
)

const (
	defaultRedisURL           = "redis://localhost:6379"
	defaultShortTTL           = 24 * time.Hour
	defaultStandardTTL        = 7 * 24 * time.Hour
	defaultAuditTTL           = 30 * 24 * time.Hour
	envArtifactTTLShort       = "ARTIFACT_TTL_SHORT"
	envArtifactTTLStandard    = "ARTIFACT_TTL_STANDARD"
	envArtifactTTLAudit       = "ARTIFACT_TTL_AUDIT"
)

// RedisStore implements artifact storage using Redis.
type RedisStore struct {
	client     *redis.Client
	ttlShort   time.Duration
	ttlStandard time.Duration
	ttlAudit   time.Duration
}

// NewRedisStore constructs an artifact store backed by Redis.
func NewRedisStore(url string) (*RedisStore, error) {
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
	return &RedisStore{
		client:      client,
		ttlShort:    parseDurationEnv(envArtifactTTLShort, defaultShortTTL),
		ttlStandard: parseDurationEnv(envArtifactTTLStandard, defaultStandardTTL),
		ttlAudit:    parseDurationEnv(envArtifactTTLAudit, defaultAuditTTL),
	}, nil
}

// Close closes the underlying Redis client.
func (s *RedisStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

// Put stores content and metadata, returning an artifact pointer.
func (s *RedisStore) Put(ctx context.Context, content []byte, meta Metadata) (string, error) {
	if s == nil || s.client == nil {
		return "", fmt.Errorf("artifact store unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id := uuid.NewString()
	key := MakeArtifactKey(id)
	metaKey := artifactMetaKey(id)
	meta.SizeBytes = int64(len(content))
	if meta.Retention == "" {
		meta.Retention = RetentionStandard
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}
	ttl := s.ttlFor(meta.Retention)
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, key, content, ttl)
	pipe.Set(ctx, metaKey, payload, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", err
	}
	return memory.PointerForKey(key), nil
}

// Get returns artifact content and metadata for a pointer.
func (s *RedisStore) Get(ctx context.Context, ptr string) ([]byte, Metadata, error) {
	if s == nil || s.client == nil {
		return nil, Metadata{}, fmt.Errorf("artifact store unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return nil, Metadata{}, err
	}
	id := strings.TrimPrefix(key, "art:")
	if id == "" {
		return nil, Metadata{}, fmt.Errorf("invalid artifact key")
	}
	metaKey := artifactMetaKey(id)
	pipe := s.client.Pipeline()
	contentCmd := pipe.Get(ctx, key)
	metaCmd := pipe.Get(ctx, metaKey)
	_, _ = pipe.Exec(ctx)

	content, err := contentCmd.Bytes()
	if err != nil {
		return nil, Metadata{}, err
	}
	var meta Metadata
	if data, err := metaCmd.Bytes(); err == nil {
		_ = json.Unmarshal(data, &meta)
	}
	return content, meta, nil
}

func (s *RedisStore) ttlFor(retention RetentionClass) time.Duration {
	switch retention {
	case RetentionShort:
		return s.ttlShort
	case RetentionAudit:
		return s.ttlAudit
	default:
		return s.ttlStandard
	}
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

// MakeArtifactKey constructs the redis key for an artifact.
func MakeArtifactKey(id string) string {
	return "art:" + id
}

func artifactMetaKey(id string) string {
	return "art:meta:" + id
}
